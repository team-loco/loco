package otelsetup

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestLogsExportDiagnosticContextAndRespectLevel(t *testing.T) {
	requests := make(chan *collectorlog.ExportLogsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request := &collectorlog.ExportLogsServiceRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", server.URL+"/v1/logs")
	provider, err := StartLogs(t.Context(), "loco-controller", "test")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	handler := LogHandler(slog.NewJSONHandler(&output, nil), provider, "loco-controller", slog.LevelInfo)
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	logger := slog.New(handler).With("resource", "synthetic-unready-service")
	logger.DebugContext(ctx, "routine reconcile")
	logger.InfoContext(
		ctx,
		"application rollout changed",
		"reason",
		"HealthCheckFailing",
		"detail",
		"readiness probe: connection refused",
	)
	if err := StopLogs(provider); err != nil {
		t.Fatal(err)
	}
	var request *collectorlog.ExportLogsServiceRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("OTLP export missing")
	}
	var count int
	var diagnostic bool
	for _, resource := range request.GetResourceLogs() {
		for _, scope := range resource.GetScopeLogs() {
			for _, record := range scope.GetLogRecords() {
				count++
				if record.GetBody().GetStringValue() != "application rollout changed" {
					continue
				}
				if hex.EncodeToString(record.GetTraceId()) != traceID.String() ||
					hex.EncodeToString(record.GetSpanId()) != spanID.String() {
					t.Fatalf("diagnostic lost trace correlation: %v", record)
				}
				diagnostic = true
				attrs := make(map[string]string)
				for _, attr := range record.GetAttributes() {
					attrs[attr.GetKey()] = attr.GetValue().GetStringValue()
				}
				if attrs["resource"] != "synthetic-unready-service" ||
					attrs["detail"] != "readiness probe: connection refused" {
					t.Fatalf("diagnostic lost context: %v", attrs)
				}
				t.Logf(
					"OTLP received trace=%s span=%s resource=%s detail=%q",
					traceID,
					spanID,
					attrs["resource"],
					attrs["detail"],
				)
			}
		}
	}
	if !diagnostic || count != 1 || bytes.Contains(output.Bytes(), []byte("routine reconcile")) {
		t.Fatalf("exported %d records, output %s; want only the diagnostic", count, &output)
	}
}
