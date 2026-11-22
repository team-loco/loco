package otelsetup

import (
	"context"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// StartMetrics installs a global meter provider exporting Go runtime metrics over OTLP/HTTP, like Start.
func StartMetrics(ctx context.Context, serviceName, version string) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithProducer(runtime.NewProducer()))
	provider, err := newMeterProvider(ctx, reader, serviceName, version)
	if err != nil {
		return nil, err
	}
	otel.SetMeterProvider(provider)
	return provider, nil
}

// StopMetrics flushes the metrics provider holds and shuts it down.
func StopMetrics(provider *sdkmetric.MeterProvider) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("flush metrics: %w", err)
	}
	return nil
}

func newMeterProvider(
	ctx context.Context, reader sdkmetric.Reader, serviceName, version string,
) (*sdkmetric.MeterProvider, error) {
	res, err := newResource(ctx, serviceName, version)
	if err != nil {
		return nil, err
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	if err = runtime.Start(runtime.WithMeterProvider(provider)); err != nil {
		return nil, fmt.Errorf("runtime metrics: %w", err)
	}
	return provider, nil
}
