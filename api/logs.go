package main

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/otelsetup"
)

const otlpLogsEndpointEnv = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"

func newLogsEnabled() bool {
	endpoint := envconfig.String(otlpEndpointEnv, "")
	logsEndpoint := envconfig.String(otlpLogsEndpointEnv, "")
	return endpoint != "" || logsEndpoint != ""
}

func startLogs(ctx context.Context, enabled bool, version string) (*sdklog.LoggerProvider, error) {
	if !enabled {
		return nil, nil
	}
	return otelsetup.StartLogs(ctx, defaultServiceName, version)
}

func stopLogs(provider *sdklog.LoggerProvider) {
	if provider == nil {
		return
	}
	if err := otelsetup.StopLogs(provider); err != nil {
		slog.ErrorContext(context.Background(), "failed to flush logs", "error", err)
	}
}

// withOTLPLogs sends every record handler writes at level or above to provider as well.
func withOTLPLogs(handler slog.Handler, provider *sdklog.LoggerProvider, level slog.Leveler) slog.Handler {
	if provider == nil {
		return handler
	}
	otlp := otelslog.NewHandler(defaultServiceName, otelslog.WithLoggerProvider(provider))
	return slog.NewMultiHandler(handler, leveledHandler{Handler: otlp, level: level})
}

type leveledHandler struct {
	slog.Handler
	level slog.Leveler
}

func (h leveledHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level() && h.Handler.Enabled(ctx, level)
}

func (h leveledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return leveledHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h leveledHandler) WithGroup(name string) slog.Handler {
	return leveledHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}
