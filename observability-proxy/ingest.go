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

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"

	"github.com/team-loco/loco/internal/clickhouse/migrationlock"
	"github.com/team-loco/loco/internal/clickhouse/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const ingestProbeConns = 1

var errIngestStarting = errors.New("ingest waiting for the clickhouse schema")

// ingestServer migrates the ClickHouse schema, then writes the OTLP telemetry it receives.
type ingestServer struct {
	probe   driver.Conn
	lock    *migrationlock.Lock
	writer  *ingest.Writer
	grpc    *grpc.Server
	http    *http.Server
	started atomic.Bool
}

func newIngestServer(
	cfg *config.Config,
	lock *migrationlock.Lock,
	proxyVersion string,
	registerer prometheus.Registerer,
) (*ingestServer, error) {
	probe, err := openProbe(cfg)
	if err != nil {
		return nil, err
	}
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(encoder, zapcore.Lock(os.Stdout), zapcore.InfoLevel)
	logger := zap.New(core)
	writer, err := ingest.NewWriter(context.Background(), cfg.Ingest(proxyVersion), logger)
	if err != nil {
		closeProbe(probe)
		return nil, err
	}
	gate := ingest.NewGate(cfg.IngestGate())
	service := ingest.NewService(writer.Consumers(), gate, cfg.IngestService(), registerer)
	server := &http.Server{
		Handler:           service.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return &ingestServer{probe: probe, lock: lock, writer: writer, grpc: service.GRPCServer(), http: server}, nil
}

func openProbe(cfg *config.Config) (driver.Conn, error) {
	opts, err := clickhouse.ParseDSN(cfg.ClickHouseIngestURL)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse ingest DSN: %w", err)
	}
	opts.Auth.Database = cfg.ClickHouseDB
	opts.MaxOpenConns = ingestProbeConns
	probe, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("connect to clickhouse for ingest: %w", err)
	}
	return probe, nil
}

// start applies the schema migrations under the migration lock, so one replica of a rollout
// migrates and the others wait, then checks the ingest user can see the writer's tables and starts
// the writer. The retry budget covers waiting for the lock as well as the migrations.
func (s *ingestServer) start(ctx context.Context, cfg *config.Config) error {
	schema := migrations.Config{
		DSN:        cfg.ClickHouseMigratorURL,
		Database:   cfg.ClickHouseDB,
		LogsTTL:    cfg.LogsTTL,
		TracesTTL:  cfg.TracesTTL,
		MetricsTTL: cfg.MetricsTTL,
	}
	budgetCtx, cancel := context.WithTimeout(ctx, cfg.MigrationRetryBudget)
	defer cancel()
	err := s.lock.WithLock(budgetCtx, func(lockCtx context.Context) error {
		return migrations.UpWithRetry(lockCtx, schema, cfg.MigrationRetryInterval)
	})
	if err != nil {
		return fmt.Errorf("migrate clickhouse schema: %w", err)
	}
	if err := ingest.CheckSchema(budgetCtx, s.probe, cfg.ClickHouseDB, s.writer.Tables()); err != nil {
		return err
	}
	if err := s.writer.Start(ctx); err != nil {
		return err
	}
	s.started.Store(true)
	return nil
}

func (s *ingestServer) serve(ctx context.Context, grpcLn, httpLn net.Listener, serveErr chan<- error) {
	go func() {
		serveErr <- s.grpc.Serve(grpcLn)
	}()
	go func() {
		serveErr <- s.http.Serve(httpLn)
	}()
	slog.InfoContext(ctx, "otlp listening", "grpc", grpcLn.Addr().String(), "http", httpLn.Addr().String())
}

func (s *ingestServer) ready(ctx context.Context) error {
	if !s.started.Load() {
		return errIngestStarting
	}
	if err := s.probe.Ping(ctx); err != nil {
		return fmt.Errorf("clickhouse ingest: %w", err)
	}
	return nil
}

func (s *ingestServer) shutdown(ctx context.Context) error {
	s.grpc.GracefulStop()
	err := s.http.Shutdown(ctx)
	if err != nil {
		err = fmt.Errorf("shut down otlp http server: %w", err)
	}
	if s.started.Load() {
		err = errors.Join(err, s.writer.Shutdown(ctx))
	}
	return err
}

func (s *ingestServer) close() {
	closeProbe(s.probe)
}

func closeProbe(probe driver.Conn) {
	if err := probe.Close(); err != nil {
		slog.Error("failed to close clickhouse connection", "error", err)
	}
}
