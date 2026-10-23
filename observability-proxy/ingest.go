package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const ingestProbeConns = 1

var errIngestStarting = errors.New("ingest waiting for the clickhouse schema")

type ingestRole struct {
	probe   *chClient.Client
	writer  *ingest.Writer
	grpc    *grpc.Server
	http    *http.Server
	started atomic.Bool
}

func newIngestRole(cfg *config.Config, proxyVersion string, registerer prometheus.Registerer) (*ingestRole, error) {
	probe, err := chClient.NewClient(cfg.ClickHouseIngestURL, cfg.ClickHouseDB, ingestProbeConns)
	if err != nil {
		return nil, fmt.Errorf("connect to clickhouse for ingest: %w", err)
	}
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), zapcore.InfoLevel)
	logger := zap.New(core)
	ingestCfg := cfg.Ingest(proxyVersion)
	writer, err := ingest.NewWriter(context.Background(), ingestCfg, logger)
	if err != nil {
		probe.Close()
		return nil, err
	}
	gate := ingest.NewGate(cfg.IngestGate())
	service := ingest.NewService(writer.Consumers(), gate, cfg.IngestService(), registerer)
	server := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return &ingestRole{probe: probe, writer: writer, grpc: service.GRPCServer(), http: server}, nil
}

func (r *ingestRole) start(ctx context.Context, cfg *config.Config) error {
	budgetCtx, cancel := context.WithTimeout(ctx, cfg.MigrationRetryBudget)
	defer cancel()
	tables := r.writer.Tables()
	if err := ingest.WaitForSchema(
		budgetCtx,
		r.probe.Conn(),
		cfg.ClickHouseDB,
		tables,
		cfg.MigrationRetryInterval,
	); err != nil {
		return err
	}
	if err := r.writer.Start(ctx); err != nil {
		return err
	}
	r.started.Store(true)
	return nil
}

func (r *ingestRole) serve(ctx context.Context, grpcLn, httpLn net.Listener, serveErr chan<- error) {
	go func() {
		serveErr <- r.grpc.Serve(grpcLn)
	}()
	go func() {
		serveErr <- r.http.Serve(httpLn)
	}()
	slog.InfoContext(ctx, "otlp listening", "grpc", grpcLn.Addr().String(), "http", httpLn.Addr().String())
}

func (r *ingestRole) ready(ctx context.Context) error {
	if !r.started.Load() {
		return errIngestStarting
	}
	if err := r.probe.Ping(ctx); err != nil {
		return fmt.Errorf("clickhouse ingest: %w", err)
	}
	return nil
}

func (r *ingestRole) shutdown(ctx context.Context) error {
	r.grpc.GracefulStop()
	err := r.http.Shutdown(ctx)
	if err != nil {
		err = fmt.Errorf("shut down otlp http server: %w", err)
	}
	if r.started.Load() {
		err = errors.Join(err, r.writer.Shutdown(ctx))
	}
	return err
}

func (r *ingestRole) close() {
	r.probe.Close()
}
