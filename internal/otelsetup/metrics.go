package otelsetup

import (
	"context"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// StartMetrics starts a meter provider exporting Go runtime metrics and those of producers over
// OTLP/HTTP, like Start.
func StartMetrics(
	ctx context.Context, serviceName, version string, producers ...sdkmetric.Producer,
) (*sdkmetric.MeterProvider, error) {
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp metric exporter: %w", err)
	}
	options := []sdkmetric.PeriodicReaderOption{runtimeProducer()}
	for _, producer := range producers {
		options = append(options, sdkmetric.WithProducer(producer))
	}
	reader := sdkmetric.NewPeriodicReader(exporter, options...)
	return newMeterProvider(ctx, reader, serviceName, version)
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

// runtimeProducer adds the runtime histograms, such as go.schedule.duration, to a reader's collections.
func runtimeProducer() sdkmetric.ReaderOption {
	return sdkmetric.WithProducer(runtime.NewProducer())
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
