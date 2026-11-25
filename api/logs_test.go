package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type recordingExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, record := range records {
		e.records = append(e.records, record.Clone())
	}
	return nil
}

func (*recordingExporter) Shutdown(context.Context) error   { return nil }
func (*recordingExporter) ForceFlush(context.Context) error { return nil }

func TestOTLPLogsReceiveWhatStdoutWritesAtTheConfiguredLevel(t *testing.T) {
	exporter := &recordingExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	var stdout bytes.Buffer
	stdoutHandler := slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(CustomHandler{Handler: withOTLPLogs(stdoutHandler, provider, slog.LevelInfo)})

	logger.With("component", "scheduler").Info("kept")
	logger.Debug("dropped")

	if lines := strings.Count(stdout.String(), "\n"); lines != 1 || !strings.Contains(stdout.String(), "kept") {
		t.Fatalf("stdout = %q, want only the info record", stdout.String())
	}
	if len(exporter.records) != 1 || exporter.records[0].Body().AsString() != "kept" {
		t.Fatalf("exported %d records, want only the info record", len(exporter.records))
	}
	var component string
	exporter.records[0].WalkAttributes(func(kv attribute.KeyValue) bool {
		if kv.Key == "component" {
			component = kv.Value.String()
		}
		return true
	})
	if component != "scheduler" {
		t.Fatalf("exported record lacks the logger's attributes")
	}
}
