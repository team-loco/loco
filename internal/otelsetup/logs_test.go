package otelsetup

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestStdoutDiagnosticPreservesCorrelationAndStructuredContext(t *testing.T) {
	var output bytes.Buffer
	handler := LogHandler(slog.NewJSONHandler(&output, nil), nil)
	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(testParentSpan)
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID,
	}))
	logger := slog.New(handler).With("resource", "synthetic-unready-service").WithGroup("rollout")
	logger.DebugContext(ctx, "routine reconcile")
	logger.InfoContext(ctx, "application rollout changed", "reason", "health_check_failing",
		"detail", "readiness probe: connection refused", "replicas", []string{"synthetic-pod"})
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["trace_id"] != traceID.String() || record["span_id"] != spanID.String() {
		t.Fatalf("stdout diagnostic lost correlation: %s", &output)
	}
	rollout, ok := record["rollout"].(map[string]any)
	if !ok || rollout["detail"] != "readiness probe: connection refused" {
		t.Fatalf("stdout diagnostic lost grouped context: %s", &output)
	}
	if record["resource"] != "synthetic-unready-service" || record["level"] != "INFO" {
		t.Fatalf("stdout diagnostic lost context: %s", &output)
	}
	t.Logf("stdout: %s", &output)
}
