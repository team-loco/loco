package otelsetup

import (
	"context"
	"fmt"

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
