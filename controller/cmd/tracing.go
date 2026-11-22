package main

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const tracingShutdownTimeout = 5 * time.Second

// startTracing installs a global tracer provider exporting to the OTLP endpoint the standard OTEL_* variables name.
func startTracing(ctx context.Context, serviceName, version string) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(rootSampler{})),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())
	return provider, nil
}

func stopTracing(provider *sdktrace.TracerProvider) error {
	ctx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("flush traces: %w", err)
	}
	return nil
}

// rootSampler drops client spans outside a reconcile: informer watches, lists and leader election renewals.
// The API keeps the same sampler in api/tracing.go.
type rootSampler struct{}

func (rootSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	parent := trace.SpanContextFromContext(p.ParentContext)
	if p.Kind == trace.SpanKindClient {
		return sdktrace.SamplingResult{Decision: sdktrace.Drop, Tracestate: parent.TraceState()}
	}
	return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample, Tracestate: parent.TraceState()}
}

func (rootSampler) Description() string {
	return "DropRootClientSpans"
}
