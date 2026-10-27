package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

const (
	testTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpan  = "00f067aa0ba902b7"
	testTraceparent = "00-" + testTraceID + "-" + testParentSpan + "-01"
	testVersion     = "sha-test"
	serviceNameEnv  = "OTEL_SERVICE_NAME"
	resourceAttrEnv = "OTEL_RESOURCE_ATTRIBUTES"
	otlpTracesPath  = "/v1/traces"
)

func recordGlobalSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	sampler := sdktrace.ParentBased(rootSampler{})
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sampler))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return recorder
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

func resourceValue(t *testing.T, res *resource.Resource, key string) string {
	t.Helper()
	for _, attr := range res.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func TestTracingResourceNamesTheService(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		attributes  string
		want        string
	}{
		{"default", "", "", defaultServiceName},
		{"OTEL_SERVICE_NAME", "api-custom", "", "api-custom"},
		{"OTEL_RESOURCE_ATTRIBUTES", "", "service.name=api-attr", "api-attr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(serviceNameEnv, tt.serviceName)
			t.Setenv(resourceAttrEnv, tt.attributes)
			res, err := newTracingResource(t.Context(), testVersion)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := resource.Merge(resource.Environment(), res)
			if err != nil {
				t.Fatal(err)
			}
			if got := resourceValue(t, merged, string(semconv.ServiceNameKey)); got != tt.want {
				t.Errorf("service.name = %q, want %q", got, tt.want)
			}
			if got := resourceValue(t, merged, string(semconv.ServiceVersionKey)); got != testVersion {
				t.Errorf("service.version = %q, want %q", got, testVersion)
			}
		})
	}
}

func TestRootSamplerDropsOnlyParentlessClientSpans(t *testing.T) {
	sampler := sdktrace.ParentBased(rootSampler{})
	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(testParentSpan)
	if err != nil {
		t.Fatal(err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	withParent := trace.ContextWithSpanContext(context.Background(), parent)
	tests := []struct {
		name string
		ctx  context.Context
		kind trace.SpanKind
		want sdktrace.SamplingDecision
	}{
		{"root client", context.Background(), trace.SpanKindClient, sdktrace.Drop},
		{"root server", context.Background(), trace.SpanKindServer, sdktrace.RecordAndSample},
		{"root internal", context.Background(), trace.SpanKindInternal, sdktrace.RecordAndSample},
		{"root producer", context.Background(), trace.SpanKindProducer, sdktrace.RecordAndSample},
		{"child client", withParent, trace.SpanKindClient, sdktrace.RecordAndSample},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sampler.ShouldSample(sdktrace.SamplingParameters{
				ParentContext: tt.ctx,
				TraceID:       traceID,
				Name:          tt.name,
				Kind:          tt.kind,
			})
			if result.Decision != tt.want {
				t.Fatalf("decision = %v, want %v", result.Decision, tt.want)
			}
		})
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
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mountRPCServers(mux, base, authenticated)
	tracedMux := withServerTracing(mux)
	srv := httptest.NewUnstartedServer(tracedMux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestRPCSpanContinuesTheIncomingTrace(t *testing.T) {
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
	if got := httpSpan.Parent().SpanID().String(); got != testParentSpan {
		t.Errorf("HTTP span parent = %s, want the incoming span %s", got, testParentSpan)
	}
	if got := rpcSpan.SpanContext().TraceID().String(); got != testTraceID {
		t.Errorf("RPC span trace = %s, want the incoming trace %s", got, testTraceID)
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
	for _, path := range []string{rootPath, healthPath} {
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
