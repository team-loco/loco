package service

import (
	"slices"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

const testImageRetention = 1

func requireRootSweepSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) {
	t.Helper()
	var root sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			root = span
		}
	}
	if root == nil {
		t.Fatalf("no %s span", name)
	}
	if root.Parent().IsValid() {
		t.Fatalf("%s span has parent %v", name, root.Parent())
	}
	ran := attribute.Bool(sweepRanAttr, true)
	if !slices.Contains(root.Attributes(), ran) {
		t.Fatalf("%s attributes = %v, want %v", name, root.Attributes(), ran)
	}
}

func TestSourceSweepRecordsARootSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newSweepFixture(t)
	if _, err := f.sweeper.tracedSweep(f.ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	requireRootSweepSpan(t, recorder, sourceSweepSpan)
}

func TestImageSweepRecordsARootSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newImageSweepFixture(t, testImageRetention)
	if _, err := f.sweeper.tracedSweep(f.ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	requireRootSweepSpan(t, recorder, imageSweepSpan)
}
