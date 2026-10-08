package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/service"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 30 * time.Second
)

var version string

type proxy struct {
	server    *http.Server
	ch        *chClient.Client
	permCache *cache.MemoryCache
}

func newProxy(cfg *config.Config) (*proxy, error) {
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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
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

	return &proxy{server: server, ch: ch, permCache: permCache}, nil
}

func (p *proxy) close() {
	p.ch.Close()
	if err := p.permCache.Close(); err != nil {
		slog.Error("failed to close permission cache", "error", err)
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
	slog.Info("starting observability proxy",
		"version", proxyVersion,
		"port", cfg.Port,
		"control_plane", cfg.ControlPlaneURL,
		"clickhouse", cfg.ClickHouseURL,
	)

	p, err := newProxy(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer p.close()

	quit := make(chan error, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		sig := <-sigChan
		slog.Info("shutdown signal received", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := p.server.Shutdown(shutdownCtx); err != nil {
			quit <- err
			return
		}
		slog.Info("server shutdown completed gracefully")
		quit <- nil
	}()

	slog.Info("server listening", "addr", p.server.Addr)
	if err := p.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", "error", err)
		return
	}

	if err := <-quit; err != nil {
		log.Fatal(err)
	}
}
