package otelsetup

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// StartLogs starts a logger provider exporting log records over OTLP/HTTP, like Start.
func StartLogs(ctx context.Context, serviceName, version string) (*sdklog.LoggerProvider, error) {
	exporter, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp log exporter: %w", err)
	}
	res, err := newResource(ctx, serviceName, version)
	if err != nil {
		return nil, err
	}
	processor := sdklog.NewBatchProcessor(exporter)
	return sdklog.NewLoggerProvider(sdklog.WithProcessor(processor), sdklog.WithResource(res)), nil
}

// StopLogs flushes the log records provider holds and shuts it down.
func StopLogs(provider *sdklog.LoggerProvider) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("flush logs: %w", err)
	}
	return nil
}

// LogHandler sends records at the configured level to both output and OTLP.
func LogHandler(output slog.Handler, provider *sdklog.LoggerProvider, name string, level slog.Leveler) slog.Handler {
	if provider == nil {
		return output
	}
	otlp := otelslog.NewHandler(name, otelslog.WithLoggerProvider(provider))
	return slog.NewMultiHandler(output, leveledHandler{Handler: otlp, level: level})
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
