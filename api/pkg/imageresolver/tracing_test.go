package imageresolver

import (
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/internal/workloadplatform"
)

func resolveSpans(spans []sdktrace.ReadOnlySpan) []sdktrace.ReadOnlySpan {
	var found []sdktrace.ReadOnlySpan
	for _, span := range spans {
		if span.Name() == resolveSpan {
			found = append(found, span)
		}
	}
	return found
}

func TestResolveGroupsItsRegistryCallsUnderOneSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	host := newRegistry(t)
	ref := pushImage(t, host, "traced/app", platformImage(t, requiredOS, requiredArchitecture))
	resolver := New(10*time.Second, 1<<30, workloadplatform.AMD64)
	if _, err := resolver.Resolve(tracingtest.StartParent(t), ref); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	spans := recorder.Ended()
	resolved := resolveSpans(spans)
	if len(resolved) != 1 {
		t.Fatalf("got %d %s spans, want 1", len(resolved), resolveSpan)
	}
	resolveID := resolved[0].SpanContext().SpanID()
	calls := 0
	for _, span := range spans {
		if span.Parent().SpanID() == resolveID {
			calls++
		}
	}
	if calls == 0 {
		t.Fatalf("no registry call is a child of %s", resolveSpan)
	}
}

func TestResolveSpanRecordsAFailedLookup(t *testing.T) {
	recorder := tracingtest.Record(t)
	ref, err := name.ParseReference(newRegistry(t) + "/missing/app:latest")
	if err != nil {
		t.Fatalf("parse reference: %v", err)
	}
	resolver := New(10*time.Second, 1<<30, workloadplatform.AMD64)
	if _, resolveErr := resolver.Resolve(tracingtest.StartParent(t), ref); resolveErr == nil {
		t.Fatal("resolve of a missing image succeeded")
	}
	resolved := resolveSpans(recorder.Ended())
	if len(resolved) != 1 || resolved[0].Status().Code != codes.Error {
		t.Fatalf("%s spans = %v, want one with error status", resolveSpan, resolved)
	}
}
