package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"

	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/observability-proxy/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
	"github.com/team-loco/loco/observability-proxy/service"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 30 * time.Second
)

var version string

var errUnknownMigrationLock = errors.New("unknown migration lock")

type migrationLock interface {
	WithLock(ctx context.Context, fn func(context.Context) error) error
}

type proxy struct {
	server    *http.Server
	ch        *chClient.Client
	permCache *cache.MemoryCache
	migrated  *atomic.Bool
	lock      migrationLock
}

func newMigrationLock(cfg *config.Config) (migrationLock, error) {
	switch cfg.MigrationLock {
	case config.MigrationLockKubernetes:
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("load in-cluster kubernetes config: %w", err)
		}
		client, err := coordinationv1client.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("create coordination client: %w", err)
		}
		return migrationlock.New(client, cfg.MigrationLease), nil
	case config.MigrationLockNone:
		return migrationlock.None{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownMigrationLock, cfg.MigrationLock)
	}
}

func newProxy(cfg *config.Config, lock migrationLock) (*proxy, error) {
	ch, err := chClient.NewClient(cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.MaxConcurrent)
	if err != nil {
		return nil, fmt.Errorf("connect to clickhouse: %w", err)
	}

	permCache, err := cache.NewMemory(cfg.TokenCacheTTL)
	if err != nil {
		ch.Close()
		return nil, fmt.Errorf("create permission cache: %w", err)
	}

	validator := auth.NewValidator(cfg.ControlPlaneURL, cfg.ProxyAuthToken, permCache)
	svc := service.NewObservabilityService(ch, cfg, validator)
	interceptors := connect.WithInterceptors(auth.NewAuthInterceptor())

	migrated := new(atomic.Bool)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !migrated.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, "clickhouse migrations pending")
			return
		}
		if err := ch.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "clickhouse: %v\n", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	path, handler := observabilityv1connect.NewObservabilityProxyServiceHandler(svc, interceptors)
	mux.Handle(path, handler)

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	addr := fmt.Sprintf(":%d", cfg.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return &proxy{server: server, ch: ch, permCache: permCache, migrated: migrated, lock: lock}, nil
}

func (p *proxy) migrate(ctx context.Context, cfg *config.Config) error {
	schema := migrations.Config{
		DSN:        cfg.ClickHouseMigratorURL,
		Database:   cfg.ClickHouseDB,
		LogsTTL:    cfg.LogsTTL,
		TracesTTL:  cfg.TracesTTL,
		MetricsTTL: cfg.MetricsTTL,
	}
	retry := migrations.Retry{Budget: cfg.MigrationRetryBudget, Interval: cfg.MigrationRetryInterval}
	budgetCtx, cancel := context.WithTimeout(ctx, cfg.MigrationRetryBudget)
	defer cancel()
	err := p.lock.WithLock(budgetCtx, func(leaseCtx context.Context) error {
		return migrations.UpWithRetry(leaseCtx, schema, retry)
	})
	if err != nil {
		return fmt.Errorf("migrate clickhouse schema: %w", err)
	}
	p.migrated.Store(true)
	return nil
}

func (p *proxy) close() {
	p.ch.Close()
	if err := p.permCache.Close(); err != nil {
		slog.Error("failed to close permission cache", "error", err)
	}
}

func logStartup(logger *slog.Logger, cfg *config.Config, proxyVersion string) {
	logger.Info("starting observability proxy",
		"version", proxyVersion,
		"port", cfg.Port,
		"clickhouse_database", cfg.ClickHouseDB,
	)
}

func (p *proxy) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := p.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server: %w", err)
	}
	return nil
}

func (p *proxy) run(ctx context.Context, cfg *config.Config, ln net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- p.server.Serve(ln)
	}()
	slog.InfoContext(ctx, "server listening", "addr", ln.Addr().String())

	if err := p.migrate(ctx, cfg); err != nil {
		if shutdownErr := p.shutdown(); shutdownErr != nil {
			slog.ErrorContext(ctx, "server shutdown failed", "error", shutdownErr)
		}
		<-serveErr
		return err
	}

	select {
	case <-ctx.Done():
		slog.InfoContext(ctx, "shutdown signal received")
		if err := p.shutdown(); err != nil {
			return err
		}
		<-serveErr
		slog.InfoContext(ctx, "server shutdown completed gracefully")
		return nil
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	}
}

func main() {
	cfg := config.Load()

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	proxyVersion := buildinfo.Version(version)
	logStartup(logger, cfg, proxyVersion)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lock, err := newMigrationLock(cfg)
	if err != nil {
		log.Fatal(err)
	}
	p, err := newProxy(cfg, lock)
	if err != nil {
		log.Fatal(err)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", p.server.Addr)
	if err != nil {
		p.close()
		log.Fatal(fmt.Errorf("listen: %w", err))
	}

	runErr := p.run(ctx, cfg, ln)
	p.close()
	if runErr != nil {
		slog.Error("observability proxy stopped", "error", runErr)
		os.Exit(1)
	}
}
