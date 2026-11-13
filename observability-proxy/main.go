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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
	"github.com/team-loco/loco/observability-proxy/service"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 30 * time.Second
	serversPerProxy   = 3
	metricsPath       = "/metrics"
)

var version string

type proxy struct {
	server *http.Server
	query  *queryRole
	ingest *ingestRole
}

type listeners struct {
	main     net.Listener
	otlpGRPC net.Listener
	otlpHTTP net.Listener
}

func newProxy(cfg *config.Config, lock *migrationlock.Lock, proxyVersion string) (*proxy, error) {
	p := &proxy{}
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
		query, err := newQueryRole(cfg, lock)
		if err != nil {
			return nil, err
		}
		mux.Handle("/", query.handler)
		p.query = query
	}
	if cfg.IngestEnabled {
		registry := prometheus.NewRegistry()
		mux.Handle("GET "+metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
		ingest, err := newIngestRole(cfg, proxyVersion, registry)
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

func (p *proxy) ready(ctx context.Context) error {
	if p.query != nil {
		if err := p.query.ready(ctx); err != nil {
			return err
		}
	}
	if p.ingest != nil {
		if err := p.ingest.ready(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *proxy) close() {
	if p.query != nil {
		p.query.close()
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
	if p.query != nil {
		if err := p.query.migrate(ctx, cfg); err != nil {
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
	logger := slog.New(service.LogHandler{Handler: handler})
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
