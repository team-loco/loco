package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

const (
	otlpEndpointEnv        = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otlpTracesEndpointEnv  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	defaultServiceName     = "loco-api"
	tracingShutdownTimeout = 5 * time.Second
	rootPath               = "/"
	healthPath             = "/health"
)

func newTracingEnabled() bool {
	endpoint := stringEnv(otlpEndpointEnv, "")
	tracesEndpoint := stringEnv(otlpTracesEndpointEnv, "")
	return endpoint != "" || tracesEndpoint != ""
}

func newTracingResource(ctx context.Context, version string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(defaultServiceName), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing resource: %w", err)
	}
	return res, nil
}

func startTracing(ctx context.Context, enabled bool, version string) (*sdktrace.TracerProvider, error) {
	if !enabled {
		return nil, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}
	res, err := newTracingResource(ctx, version)
	if err != nil {
		return nil, err
	}
	sampler := sdktrace.ParentBased(rootSampler{})
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(provider)
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	otel.SetTextMapPropagator(propagator)
	return provider, nil
}

func stopTracing(provider *sdktrace.TracerProvider) {
	if provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to flush traces", "error", err)
	}
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

func traceHTTPRequest(r *http.Request) bool {
	switch r.URL.Path {
	case rootPath:
		return false
	case healthPath:
		return false
	case agentv1connect.AgentServiceSyncProcedure:
		return false
	default:
		return true
	}
}

func traceRPC(_ context.Context, spec connect.Spec) bool {
	return spec.Procedure != agentv1connect.AgentServiceSyncProcedure
}

func withServerTracing(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, defaultServiceName, otelhttp.WithFilter(traceHTTPRequest))
}

func newTracingInterceptor() (connect.ServerInterceptor, error) {
	tracingInterceptor, err := otelconnect.NewServerInterceptor(
		otelconnect.WithoutMetrics(),
		otelconnect.WithFilter(traceRPC),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing interceptor: %w", err)
	}
	return tracingInterceptor, nil
}
