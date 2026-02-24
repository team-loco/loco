package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/service"
	"github.com/team-loco/loco/proto/loco/observability/v1/observabilityv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("starting observability proxy",
		"port", cfg.Port,
		"control_plane", cfg.ControlPlaneURL,
		"clickhouse", cfg.ClickHouseURL,
	)

	// Initialize ClickHouse client
	ch, err := chClient.NewClient(cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.MaxConcurrent)
	if err != nil {
		log.Fatalf("failed to connect to clickhouse: %v", err)
	}
	defer ch.Close()

	if err := ch.Ping(context.Background()); err != nil {
		slog.Warn("clickhouse ping failed on startup (may not be ready yet)", "error", err)
	}

	// Initialize token validator
	validator := auth.NewValidator(cfg.ControlPlaneURL, cfg.ProxyAuthToken, cfg.TokenCacheTTL)

	// Initialize service
	svc := service.NewObservabilityService(ch, cfg)

	// Build mux with auth interceptor (handles both unary and streaming)
	interceptors := connect.WithInterceptors(auth.NewAuthInterceptor(validator))

	mux := http.NewServeMux()

	// Health endpoints (no auth)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
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

	// ConnectRPC service
	path, handler := observabilityv1connect.NewObservabilityProxyServiceHandler(svc, interceptors)
	mux.Handle(path, handler)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	// Graceful shutdown
	quit := make(chan error, 1)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigChan)

	go func() {
		sig := <-sigChan
		slog.Info("shutdown signal received", "signal", sig.String())

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			quit <- err
			return
		}
		slog.Info("server shutdown completed gracefully")
		quit <- nil
	}()

	slog.Info("server listening", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		return
	}

	if err := <-quit; err != nil {
		log.Fatal(err)
	}
}
