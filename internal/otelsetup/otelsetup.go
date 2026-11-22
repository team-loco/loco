// Package otelsetup installs the OpenTelemetry providers Loco's servers export their own traces and metrics with.
package otelsetup

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

const shutdownTimeout = 5 * time.Second

// Start installs a global tracer provider exporting over OTLP/HTTP to the endpoint the OTEL_* variables name.
func Start(ctx context.Context, serviceName, version string) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	res, err := newResource(ctx, serviceName, version)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(Sampler()),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())
	return provider, nil
}

// Stop flushes the spans provider holds and shuts it down.
func Stop(provider *sdktrace.TracerProvider) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("flush traces: %w", err)
	}
	return nil
}

// Sampler follows the parent's decision and drops client spans without one, such as background polls and watches.
func Sampler() sdktrace.Sampler {
	return sdktrace.ParentBased(rootSampler{})
}

func newResource(ctx context.Context, serviceName, version string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	return res, nil
}

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
