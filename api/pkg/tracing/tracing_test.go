package tracing_test

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/pkg/tracingtest"
)

const rootSpanName = "work"

var errWork = errors.New("work failed")

func TestTraceparentRoundTripsThroughALink(t *testing.T) {
	tracingtest.Record(t)
	ctx := tracingtest.StartParent(t)
	traceparent := tracing.Traceparent(ctx)
	link, ok := tracing.LinkTo(traceparent)
	if !ok {
		t.Fatalf("LinkTo(%q) is invalid", traceparent)
	}
	want := trace.SpanContextFromContext(ctx)
	if link.SpanContext.TraceID() != want.TraceID() || link.SpanContext.SpanID() != want.SpanID() {
		t.Fatalf("link = %v, want %v", link.SpanContext, want)
	}
}

func TestTraceparentIsEmptyWithoutASpan(t *testing.T) {
	if got := tracing.Traceparent(t.Context()); got != "" {
		t.Fatalf("traceparent = %q, want empty", got)
	}
	if _, ok := tracing.LinkTo(""); ok {
		t.Fatal("LinkTo(\"\") is valid")
	}
}

func TestStartRootIgnoresTheCallersSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	ctx := tracingtest.StartParent(t)
	_, span := tracing.StartRoot(ctx, rootSpanName)
	tracing.End(span, errWork)
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	root := ended[0]
	if root.Parent().IsValid() {
		t.Fatalf("root span has parent %v", root.Parent())
	}
	if root.Status().Code != codes.Error || root.Status().Description != errWork.Error() {
		t.Fatalf("status = %+v, want the error", root.Status())
	}
}
