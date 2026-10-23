package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/clickhouseexporter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

const (
	buildCommand      = "loco-obs-proxy"
	compression       = "lz4"
	dialTimeoutParam  = "dial_timeout"
	logQueriesParam   = "log_queries"
	logQueriesDisable = "0"
)

var errExporterConfig = errors.New("clickhouse exporter returned an unexpected config type")

type Config struct {
	DSN         string
	Database    string
	Timeout     time.Duration
	DialTimeout time.Duration
	Version     string
}

type Writer struct {
	logs    exporter.Logs
	traces  exporter.Traces
	metrics exporter.Metrics
	tables  []string
}

type host struct{}

func (host) GetExtensions() map[component.ID]component.Component {
	return nil
}

func exporterConfig(factory exporter.Factory, cfg Config) (*clickhouseexporter.Config, error) {
	exporterCfg, ok := factory.CreateDefaultConfig().(*clickhouseexporter.Config)
	if !ok {
		return nil, errExporterConfig
	}
	exporterCfg.Endpoint = cfg.DSN
	exporterCfg.Database = cfg.Database
	exporterCfg.CreateSchema = false
	exporterCfg.AsyncInsert = true
	exporterCfg.Compress = compression
	exporterCfg.ConnectionParams = map[string]string{
		dialTimeoutParam: cfg.DialTimeout.String(),
		logQueriesParam:  logQueriesDisable,
	}
	exporterCfg.TimeoutSettings.Timeout = cfg.Timeout
	exporterCfg.QueueSettings = configoptional.None[exporterhelper.QueueBatchConfig]()
	exporterCfg.BackOffConfig.Enabled = false
	if err := exporterCfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate clickhouse exporter config: %w", err)
	}
	return exporterCfg, nil
}

func NewWriter(ctx context.Context, cfg Config, logger *zap.Logger) (*Writer, error) {
	factory := clickhouseexporter.NewFactory()
	exporterCfg, err := exporterConfig(factory, cfg)
	if err != nil {
		return nil, err
	}
	telemetry := component.TelemetrySettings{
		Logger:         logger,
		TracerProvider: tracenoop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Resource:       pcommon.NewResource(),
	}
	settings := exporter.Settings{
		ID:                component.NewID(factory.Type()),
		TelemetrySettings: telemetry,
		BuildInfo:         component.BuildInfo{Command: buildCommand, Version: cfg.Version},
	}
	logs, err := factory.CreateLogs(ctx, settings, exporterCfg)
	if err != nil {
		return nil, fmt.Errorf("create logs exporter: %w", err)
	}
	traces, err := factory.CreateTraces(ctx, settings, exporterCfg)
	if err != nil {
		return nil, fmt.Errorf("create traces exporter: %w", err)
	}
	metrics, err := factory.CreateMetrics(ctx, settings, exporterCfg)
	if err != nil {
		return nil, fmt.Errorf("create metrics exporter: %w", err)
	}
	tables := []string{
		exporterCfg.LogsTableName,
		exporterCfg.TracesTableName,
		exporterCfg.MetricsTables.Gauge.Name,
		exporterCfg.MetricsTables.Sum.Name,
		exporterCfg.MetricsTables.Summary.Name,
		exporterCfg.MetricsTables.Histogram.Name,
		exporterCfg.MetricsTables.ExponentialHistogram.Name,
	}
	return &Writer{logs: logs, traces: traces, metrics: metrics, tables: tables}, nil
}

func (w *Writer) Tables() []string {
	return w.tables
}

func (w *Writer) Consumers() Consumers {
	return Consumers{Logs: w.logs, Traces: w.traces, Metrics: w.metrics}
}

func (w *Writer) Start(ctx context.Context) error {
	for _, exp := range []component.Component{w.logs, w.traces, w.metrics} {
		if err := exp.Start(ctx, host{}); err != nil {
			return fmt.Errorf("start clickhouse exporter: %w", err)
		}
	}
	return nil
}

func (w *Writer) Shutdown(ctx context.Context) error {
	var err error
	for _, exp := range []component.Component{w.logs, w.traces, w.metrics} {
		err = errors.Join(err, exp.Shutdown(ctx))
	}
	return err
}
