package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/otelsetup"
)

const (
	testTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpan  = "00f067aa0ba902b7"
	testTraceparent = "00-" + testTraceID + "-" + testParentSpan + "-00"
	testVersion     = "sha-test"
	otlpTracesPath  = "/v1/traces"
)

func recordGlobalSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sampler := otelsetup.Sampler()
	return tracingtest.Record(t, sdktrace.WithSampler(sampler))
}

func TestNewAPIConfigEnablesTracingWithAnOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{"no endpoint", "", false},
		{"shared endpoint", otlpEndpointEnv, true},
		{"traces endpoint", otlpTracesEndpointEnv, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			if tt.env != "" {
				t.Setenv(tt.env, "http://collector.loco.test:4318")
			}
			if got := newAPIConfig().TracingEnabled; got != tt.want {
				t.Fatalf("tracing enabled = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestStartTracingDisabledInstallsNothing(t *testing.T) {
	previous := otel.GetTracerProvider()
	provider, err := startTracing(t.Context(), false, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if provider != nil {
		t.Fatal("startTracing returned a provider while disabled")
	}
	if otel.GetTracerProvider() != previous {
		t.Fatal("startTracing replaced the global tracer provider while disabled")
	}
}

func TestStartTracingExportsOverOTLP(t *testing.T) {
	var exported atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == otlpTracesPath {
			exported.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	t.Setenv(otlpEndpointEnv, collector.URL)
	recordGlobalSpans(t)

	provider, err := startTracing(t.Context(), true, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetTracerProvider() != provider {
		t.Fatal("startTracing did not install its provider globally")
	}
	_, span := otel.Tracer("test").Start(t.Context(), "work", trace.WithSpanKind(trace.SpanKindServer))
	span.End()
	stopTracing(provider)
	if exported.Load() == 0 {
		t.Fatal("no spans reached the OTLP endpoint")
	}
}

type tracedAgent struct {
	agentv1connect.UnimplementedAgentServiceHandler
}

func newTracedServer(t *testing.T) *httptest.Server {
	t.Helper()
	tracingInterceptor, err := newTracingInterceptor()
	if err != nil {
		t.Fatal(err)
	}
	base := connect.NewServer(tracingInterceptor)
	authenticated := connect.NewServer(tracingInterceptor)
	configv1connect.RegisterConfigServiceHandler(base, &publicConfig{})
	agentv1connect.RegisterAgentServiceHandler(base, &tracedAgent{})
	mux := http.NewServeMux()
	mux.HandleFunc(rootPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc(livePath, liveHandler)
	mux.HandleFunc(readyPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mountRPCServers(mux, base, authenticated)
	tracedMux := withServerTracing(mux)
	srv := httptest.NewUnstartedServer(tracedMux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestRPCSpanStartsItsOwnTraceAndLinksTheCaller(t *testing.T) {
	recorder := recordGlobalSpans(t)
	srv := newTracedServer(t)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := configv1connect.NewConfigServiceClient(connect.NewClient(transport))
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("traceparent", testTraceparent)
	if _, err := client.GetConfig(ctx, &configv1.GetConfigRequest{}); err != nil {
		t.Fatal(err)
	}

	spans := recorder.Ended()
	var httpSpan, rpcSpan sdktrace.ReadOnlySpan
	for _, span := range spans {
		switch span.Name() {
		case configv1connect.ConfigServiceGetConfigProcedure[1:]:
			rpcSpan = span
		default:
			httpSpan = span
		}
	}
	if len(spans) != 2 || rpcSpan == nil || httpSpan == nil {
		t.Fatalf("spans = %v, want one HTTP span and one RPC span", spanNames(spans))
	}
	if httpSpan.Parent().IsValid() || httpSpan.SpanContext().TraceID().String() == testTraceID {
		t.Errorf("HTTP span joined the caller's trace %s instead of starting its own", testTraceID)
	}
	links := httpSpan.Links()
	if len(links) != 1 || links[0].SpanContext.SpanID().String() != testParentSpan {
		t.Errorf("HTTP span links = %v, want the caller's span %s", links, testParentSpan)
	}
	rpcParent := rpcSpan.Parent().SpanID()
	httpSpanID := httpSpan.SpanContext().SpanID()
	if rpcParent != httpSpanID {
		t.Errorf("RPC span parent = %s, want the HTTP span %s", rpcParent, httpSpanID)
	}
}

func TestUntracedRoutesRecordNoSpans(t *testing.T) {
	recorder := recordGlobalSpans(t)
	srv := newTracedServer(t)
	for _, path := range []string{rootPath, livePath, readyPath} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	agent := agentv1connect.NewAgentServiceClient(connect.NewClient(transport))
	stream, err := agent.Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if closeErr := stream.CloseSend(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, recvErr := stream.Receive(); connect.CodeOf(recvErr) != connect.CodeUnimplemented {
		t.Fatalf("sync error = %v, want the handler's unimplemented error", recvErr)
	}
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("spans = %v, want none", spanNames(spans))
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	return names
}

func TestOutboundHTTPClientPropagatesTraceContext(t *testing.T) {
	tracingtest.Record(t)
	server := tracingtest.NewHeaderServer(t, http.StatusNoContent)
	ctx := tracingtest.StartParent(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := newOutboundHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	tracingtest.AssertContinues(t, server.Traceparent(), ctx)
}
