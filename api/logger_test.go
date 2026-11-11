package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/team-loco/loco/api/reqctx"
	"go.opentelemetry.io/otel/trace"
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
	ctx := reqctx.WithRequest(t.Context(), reqctx.Request{ID: testRequestID})
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

func TestDerivedLoggerPreservesContext(t *testing.T) {
	for _, tt := range []struct {
		name   string
		derive func(*slog.Logger) *slog.Logger
		group  string
	}{
		{name: "With", derive: func(l *slog.Logger) *slog.Logger { return l.With("component", "test") }},
		{name: "WithGroup", derive: func(l *slog.Logger) *slog.Logger { return l.WithGroup("worker") }, group: "worker"},
		{
			name: "WithGroupAndWith",
			derive: func(l *slog.Logger) *slog.Logger {
				return l.WithGroup("worker").With("component", "test")
			},
			group: "worker",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := tt.derive(slog.New(CustomHandler{Handler: slog.NewJSONHandler(&buf, nil)}))
			ctx := reqctx.WithRequest(t.Context(), reqctx.Request{ID: testRequestID})
			ctx = trace.ContextWithSpanContext(ctx, testSpanContext(t))
			logger.InfoContext(ctx, "hello", "answer", 42)
			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatal(err)
			}
			if tt.group != "" {
				group, ok := line[tt.group].(map[string]any)
				if !ok {
					t.Fatalf("group %q missing: %v", tt.group, line)
				}
				line = group
			}
			request, ok := line["request"].(map[string]any)
			if !ok || request["requestId"] != testRequestID {
				t.Errorf("request group = %v, want requestId %s", line["request"], testRequestID)
			}
			if line["trace_id"] != testTraceID || line["span_id"] != testParentSpan {
				t.Errorf("trace fields = %v, want trace_id %s and span_id %s", line, testTraceID, testParentSpan)
			}
			if tt.name != "WithGroup" && line["component"] != "test" {
				t.Errorf("component = %v, want test", line["component"])
			}
			if line["answer"] != float64(42) {
				t.Errorf("answer = %v, want 42", line["answer"])
			}
		})
	}
}
