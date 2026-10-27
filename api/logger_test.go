package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/contextkeys"
)

const testRequestID = "req-1"

func logLine(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	jsonHandler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(CustomHandler{Handler: jsonHandler})
	logger.InfoContext(ctx, "hello")
	line := map[string]any{}
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	return line
}

func testSpanContext(t *testing.T) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(testTraceID)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(testParentSpan)
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID})
}

func TestLoggerAddsTraceAndSpanIDs(t *testing.T) {
	spanContext := testSpanContext(t)
	ctx := trace.ContextWithSpanContext(t.Context(), spanContext)
	line := logLine(t, ctx)
	if line["trace_id"] != testTraceID {
		t.Errorf("trace_id = %v, want %s", line["trace_id"], testTraceID)
	}
	if line["span_id"] != testParentSpan {
		t.Errorf("span_id = %v, want %s", line["span_id"], testParentSpan)
	}
	if _, ok := line["request"]; ok {
		t.Errorf("request group present without a request ID: %v", line)
	}
}

func TestLoggerOmitsTraceIDsWithoutASpan(t *testing.T) {
	ctx := context.WithValue(t.Context(), contextkeys.RequestIDKey, testRequestID)
	line := logLine(t, ctx)
	if _, ok := line["trace_id"]; ok {
		t.Errorf("trace_id present without a span: %v", line)
	}
	request, ok := line["request"].(map[string]any)
	if !ok {
		t.Fatalf("request group missing: %v", line)
	}
	if request["requestId"] != testRequestID {
		t.Errorf("requestId = %v, want %s", request["requestId"], testRequestID)
	}
}
