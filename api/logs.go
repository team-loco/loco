package main

import (
	"context"
	"log/slog"

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
