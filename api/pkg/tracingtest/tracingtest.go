package tracingtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName        = "github.com/team-loco/loco/api/pkg/tracingtest"
	parentSpanName    = "parent"
	traceparentName   = "traceparent"
	traceparentHeader = "Traceparent"
)

func Record(t *testing.T, opts ...sdktrace.TracerProviderOption) *tracetest.SpanRecorder {
	t.Helper()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	opts = append(opts, sdktrace.WithSpanProcessor(recorder))
	provider := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return recorder
}

func StartParent(t *testing.T) context.Context {
	t.Helper()
	ctx, span := otel.Tracer(tracerName).Start(t.Context(), parentSpanName, trace.WithSpanKind(trace.SpanKindServer))
	t.Cleanup(func() { span.End() })
	return ctx
}

type HeaderServer struct {
	*httptest.Server
	mu          sync.Mutex
	traceparent string
}

func NewHeaderServer(t *testing.T, status int) *HeaderServer {
	t.Helper()
	server := &HeaderServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.traceparent = r.Header.Get(traceparentHeader)
		server.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *HeaderServer) Traceparent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.traceparent
}

func AssertContinues(t *testing.T, traceparent string, parent context.Context) {
	t.Helper()
	traceID := trace.SpanContextFromContext(parent).TraceID().String()
	if traceparent == "" {
		t.Fatal("request carried no traceparent")
	}
	carrier := propagation.MapCarrier{traceparentName: traceparent}
	extracted := propagation.TraceContext{}.Extract(context.Background(), carrier)
	got := trace.SpanContextFromContext(extracted).TraceID().String()
	if got != traceID {
		t.Fatalf("traceparent %q continues trace %s, want %s", traceparent, got, traceID)
	}
}
