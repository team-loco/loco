package main

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/otelsetup"
)

const (
	otlpMetricsEndpointEnv = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	meterName              = "github.com/team-loco/loco/api"
)

func newMetricsEnabled() bool {
	endpoint := envconfig.String(otlpEndpointEnv, "")
	metricsEndpoint := envconfig.String(otlpMetricsEndpointEnv, "")
	return endpoint != "" || metricsEndpoint != ""
}

func startMetrics(ctx context.Context, enabled bool, version string) (*sdkmetric.MeterProvider, error) {
	if !enabled {
		return nil, nil
	}
	return otelsetup.StartMetrics(ctx, defaultServiceName, version)
}

func stopMetrics(provider *sdkmetric.MeterProvider) {
	if provider == nil {
		return
	}
	if err := otelsetup.StopMetrics(provider); err != nil {
		slog.ErrorContext(context.Background(), "failed to flush metrics", "error", err)
	}
}

// apiMeter records the API's own instruments on provider, or nowhere while metrics are off.
func apiMeter(provider *sdkmetric.MeterProvider) metric.Meter {
	if provider == nil {
		return noop.NewMeterProvider().Meter(meterName)
	}
	return provider.Meter(meterName)
}
