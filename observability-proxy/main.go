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

	"connectrpc.com/connect/v2/connecthttp"

	"connectrpc.com/connect/v2"

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
	serversPerProxy   = 3
)

var version string

var errMigrationsPending = errors.New("clickhouse migrations pending")

type migrationLock interface {
	WithLock(ctx context.Context, fn func(context.Context) error) error
}

type proxy struct {
	server    *http.Server
	ch        *chClient.Client
	permCache *cache.MemoryCache
	migrated  *atomic.Bool
	lock      migrationLock
	ingest    *ingestRole
}

type listeners struct {
	main     net.Listener
	otlpGRPC net.Listener
	otlpHTTP net.Listener
}

func newProxy(cfg *config.Config, lock migrationLock, proxyVersion string) (*proxy, error) {
	p := &proxy{migrated: new(atomic.Bool), lock: lock}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := p.ready(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	if cfg.QueryEnabled {
		if err := p.mountQuery(cfg, mux); err != nil {
			return nil, err
		}
	}
	if cfg.IngestEnabled {
		ingest, err := newIngestRole(cfg, proxyVersion)
		if err != nil {
			p.close()
			return nil, err
		}
		p.ingest = ingest
	}

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	p.server = &http.Server{
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return p, nil
}

func (p *proxy) mountQuery(cfg *config.Config, mux *http.ServeMux) error {
	ch, err := chClient.NewClient(cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.MaxConcurrent)
	if err != nil {
		return fmt.Errorf("connect to clickhouse: %w", err)
	}
	permCache, err := cache.NewMemory(cfg.TokenCacheTTL)
	if err != nil {
		ch.Close()
		return fmt.Errorf("create permission cache: %w", err)
	}
	p.ch = ch
	p.permCache = permCache

	validator := auth.NewValidator(cfg.ControlPlaneURL, permCache)
	svc := service.NewObservabilityService(ch, cfg, validator)
	rpcServer := connect.NewServer(auth.NewAuthInterceptor().Interceptor())
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(rpcServer, svc)
	rpcMux := http.NewServeMux()
	connecthttp.Mount(rpcMux, rpcServer)
	corsHandler := withCORS(cfg.CORSAllowedOrigins, rpcMux)
	mux.Handle("/", corsHandler)
	return nil
}

func (p *proxy) ready(ctx context.Context) error {
	if p.ch != nil {
		if !p.migrated.Load() {
			return errMigrationsPending
		}
		if err := p.ch.Ping(ctx); err != nil {
			return fmt.Errorf("clickhouse: %w", err)
		}
	}
	if p.ingest != nil {
		if err := p.ingest.ready(ctx); err != nil {
			return err
		}
	}
	return nil
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
	err := p.lock.WithLock(budgetCtx, func(lockCtx context.Context) error {
		return migrations.UpWithRetry(lockCtx, schema, retry)
	})
	if err != nil {
		return fmt.Errorf("migrate clickhouse schema: %w", err)
	}
	p.migrated.Store(true)
	return nil
}

func (p *proxy) close() {
	if p.ch != nil {
		p.ch.Close()
	}
	if p.permCache != nil {
		if err := p.permCache.Close(); err != nil {
			slog.Error("failed to close permission cache", "error", err)
		}
	}
	if p.ingest != nil {
		p.ingest.close()
	}
}

func logStartup(logger *slog.Logger, cfg *config.Config, proxyVersion string) {
	logger.Info("starting observability proxy",
		"version", proxyVersion,
		"port", cfg.Port,
		"query_enabled", cfg.QueryEnabled,
		"ingest_enabled", cfg.IngestEnabled,
		"clickhouse_database", cfg.ClickHouseDB,
	)
}

func (p *proxy) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var err error
	if p.ingest != nil {
		err = p.ingest.shutdown(shutdownCtx)
	}
	if shutdownErr := p.server.Shutdown(shutdownCtx); shutdownErr != nil {
		err = errors.Join(err, fmt.Errorf("shut down server: %w", shutdownErr))
	}
	return err
}

func (p *proxy) start(ctx context.Context, cfg *config.Config, ln listeners, serveErr chan<- error) error {
	if p.ch != nil {
		if err := p.migrate(ctx, cfg); err != nil {
			return err
		}
	}
	if p.ingest != nil {
		if err := p.ingest.start(ctx, cfg); err != nil {
			return err
		}
		p.ingest.serve(ctx, ln.otlpGRPC, ln.otlpHTTP, serveErr)
	}
	return nil
}

func (p *proxy) run(ctx context.Context, cfg *config.Config, ln listeners) error {
	serveErr := make(chan error, serversPerProxy)
	go func() {
		serveErr <- p.server.Serve(ln.main)
	}()
	slog.InfoContext(ctx, "server listening", "addr", ln.main.Addr().String())

	if err := p.start(ctx, cfg, ln, serveErr); err != nil {
		if shutdownErr := p.shutdown(); shutdownErr != nil {
			slog.ErrorContext(ctx, "server shutdown failed", "error", shutdownErr)
		}
		return err
	}

	select {
	case <-ctx.Done():
		slog.InfoContext(ctx, "shutdown signal received")
		if err := p.shutdown(); err != nil {
			return err
		}
		slog.InfoContext(ctx, "server shutdown completed gracefully")
		return nil
	case err := <-serveErr:
		if shutdownErr := p.shutdown(); shutdownErr != nil {
			slog.ErrorContext(ctx, "server shutdown failed", "error", shutdownErr)
		}
		return fmt.Errorf("serve: %w", err)
	}
}

func listen(ctx context.Context, port int) (net.Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen on %d: %w", port, err)
	}
	return ln, nil
}

func openListeners(ctx context.Context, cfg *config.Config) (listeners, error) {
	var ln listeners
	var err error
	if ln.main, err = listen(ctx, cfg.Port); err != nil {
		return ln, err
	}
	if !cfg.IngestEnabled {
		return ln, nil
	}
	if ln.otlpGRPC, err = listen(ctx, cfg.OTLPGRPCPort); err != nil {
		return ln, err
	}
	if ln.otlpHTTP, err = listen(ctx, cfg.OTLPHTTPPort); err != nil {
		return ln, err
	}
	return ln, nil
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

	lock := migrationlock.New(cfg.MigrationLock())
	p, err := newProxy(cfg, lock, proxyVersion)
	if err != nil {
		log.Fatal(err)
	}

	ln, err := openListeners(ctx, cfg)
	if err != nil {
		p.close()
		log.Fatal(err)
	}

	runErr := p.run(ctx, cfg, ln)
	p.close()
	if runErr != nil {
		slog.Error("observability proxy stopped", "error", runErr)
		os.Exit(1)
	}
}
