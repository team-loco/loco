package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const (
	testReconnectDelay      = time.Millisecond
	testHealthyStreamWindow = time.Hour
	testStreamName          = "test stream"
	testAttemptsBeforeStop  = 2
)

var errTestStreamDropped = errors.New("stream dropped")

func TestReconnectLoopLogsARetriedStreamErrorAsAWarning(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	a := &Agent{cfg: &Config{
		ReconnectBaseDelay:    testReconnectDelay,
		ReconnectMaxDelay:     testReconnectDelay,
		HealthyStreamDuration: testHealthyStreamWindow,
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	err := a.reconnectLoop(ctx, testStreamName, func(context.Context) error {
		attempts++
		if attempts == testAttemptsBeforeStop {
			cancel()
		}
		return errTestStreamDropped
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reconnectLoop returned %v, want %v", err, context.Canceled)
	}

	output := strings.TrimSpace(logs.String())
	lines := strings.Split(output, "\n")
	if len(lines) != 1 {
		t.Fatalf("logged %q, want one record for the one retried attempt", output)
	}
	var record struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode %q: %v", lines[0], err)
	}
	want := testStreamName + " error, reconnecting"
	if record.Level != slog.LevelWarn.String() || record.Msg != want ||
		record.Error != errTestStreamDropped.Error() {
		t.Fatalf("logged %+v, want a %s record of the dropped stream", record, slog.LevelWarn)
	}
}
