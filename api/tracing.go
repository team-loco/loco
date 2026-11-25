package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/otelsetup"
)

const (
	otlpEndpointEnv       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otlpTracesEndpointEnv = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	defaultServiceName    = "loco-api"
	rootPath              = "/"
)

func newTracingEnabled() bool {
	endpoint := envconfig.String(otlpEndpointEnv, "")
	tracesEndpoint := envconfig.String(otlpTracesEndpointEnv, "")
	return endpoint != "" || tracesEndpoint != ""
}

func startTracing(ctx context.Context, enabled bool, version string) (*sdktrace.TracerProvider, error) {
	if !enabled {
		return nil, nil
	}
	return otelsetup.Start(ctx, defaultServiceName, version)
}

func stopTracing(provider *sdktrace.TracerProvider) {
	if provider == nil {
		return
	}
	if err := otelsetup.Stop(provider); err != nil {
		slog.ErrorContext(context.Background(), "failed to flush traces", "error", err)
	}
}

func traceHTTPRequest(r *http.Request) bool {
	switch r.URL.Path {
	case rootPath:
		return false
	case livePath, readyPath, healthPath:
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

// untrustedCaller starts every request's trace in the API and links it to the caller's span.
func untrustedCaller(*http.Request) bool {
	return true
}

func withServerTracing(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, defaultServiceName,
		otelhttp.WithFilter(traceHTTPRequest),
		otelhttp.WithPublicEndpointFn(untrustedCaller),
	)
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
