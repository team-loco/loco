package otelsetup

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	testServiceName = "loco-test"
	testVersion     = "sha-test"
	testTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpan  = "00f067aa0ba902b7"
	serviceNameEnv  = "OTEL_SERVICE_NAME"
	resourceAttrEnv = "OTEL_RESOURCE_ATTRIBUTES"
)

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
		{"default", "", "", testServiceName},
		{"OTEL_SERVICE_NAME", "api-custom", "", "api-custom"},
		{"OTEL_RESOURCE_ATTRIBUTES", "", "service.name=api-attr", "api-attr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(serviceNameEnv, tt.serviceName)
			t.Setenv(resourceAttrEnv, tt.attributes)
			res, err := newResource(t.Context(), testServiceName, testVersion)
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
	sampler := Sampler()
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
