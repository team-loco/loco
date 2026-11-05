package service

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var errTailTestSend = errors.New("tail receiver disconnected")

const (
	tailTestEnvironment = "66666666-7777-8888-9999-000000000000"
	tailTestWorkspace   = "11111111-2222-3333-4444-555555555555"
	tailTestBurstSize   = tailPageSize*2 + 1
)

func newTailTestService(t *testing.T) (*ObservabilityService, *chClient.Client) {
	t.Helper()
	client := newTestClickHouse(t)
	return NewObservabilityService(client, &config.Config{}, nil), client
}

func TestTailPollDrainsEqualTimestampPagesAndKeepsItsCursor(t *testing.T) {
	service, client := newTailTestService(t)

	timestamp := time.Now().UTC().Add(-time.Minute)
	attrs := map[string]string{
		counterHTTPWorkspaceAttribute: tailTestWorkspace,
		"loco.environment.id":         tailTestEnvironment,
	}
	insert := func(count int) {
		t.Helper()
		batch, err := client.Conn().
			PrepareBatch(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes)")
		if err != nil {
			t.Fatal(err)
		}
		for range count {
			if err := batch.Append(timestamp, "same log", attrs); err != nil {
				t.Fatal(err)
			}
		}
		if err := batch.Send(); err != nil {
			t.Fatal(err)
		}
	}
	insert(tailTestBurstSize)
	foreign := map[string]string{counterHTTPWorkspaceAttribute: tailTestWorkspace}
	insertSQL := "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes) VALUES (?, ?, ?)"
	if err := client.Conn().
		Exec(t.Context(), insertSQL, timestamp, "build log", foreign); err != nil {
		t.Fatal(err)
	}
	request := &observabilityv1.TailLogsRequest{WorkspaceId: tailTestWorkspace, EnvironmentId: tailTestEnvironment}
	cursor := chClient.TailLogCursor{}
	var sends int
	var sendErr error
	send := func(*observabilityv1.LogEntry) error {
		if sendErr != nil {
			return sendErr
		}
		sends++
		return nil
	}
	poll := func(want int) {
		t.Helper()
		sent, err := service.pollTailLogs(t.Context(), tailLogQuery(request, timestamp), cursor, send)
		if err != nil {
			t.Fatal(err)
		}
		if sent != want {
			t.Fatalf("poll delivered %d entries, want %d", sent, want)
		}
	}
	poll(tailTestBurstSize)
	if len(cursor) == 0 {
		t.Fatal("tail has no resume cursor after draining its final page")
	}
	poll(0)
	insert(1)
	poll(1)
	poll(0)
	if sends != tailTestBurstSize+1 {
		t.Fatalf("sent %d entries, want %d", sends, tailTestBurstSize+1)
	}
	insert(1)
	before := maps.Clone(cursor)
	sendErr = errTailTestSend
	if _, err := service.pollTailLogs(
		t.Context(),
		tailLogQuery(request, timestamp),
		cursor,
		send,
	); !errors.Is(
		err,
		errTailTestSend,
	) {
		t.Fatalf("send error = %v", err)
	}
	if !maps.Equal(cursor, before) {
		t.Fatal("failed send advanced the cursor")
	}
	sendErr = nil
	poll(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.pollTailLogs(
		ctx,
		tailLogQuery(request, timestamp),
		cursor,
		send,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("canceled poll error = %v", err)
	}
}

func TestTailPollDeliversLateRecordsAcrossPartitionsAndMerges(t *testing.T) {
	service, client := newTailTestService(t)
	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	since := midnight.Add(-time.Minute)
	first := time.Now().UTC()
	insert := func(timestamp time.Time, body string) {
		t.Helper()
		batch, err := client.Conn().
			PrepareBatch(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes)")
		if err != nil {
			t.Fatal(err)
		}
		if err := batch.Append(
			timestamp,
			body,
			map[string]string{counterHTTPWorkspaceAttribute: tailTestWorkspace},
		); err != nil {
			t.Fatal(err)
		}
		if err := batch.Send(); err != nil {
			t.Fatal(err)
		}
	}
	request := &observabilityv1.TailLogsRequest{WorkspaceId: tailTestWorkspace}
	cursor := chClient.TailLogCursor{}
	var bodies []string
	poll := func(want string) {
		t.Helper()
		bodies = nil
		_, err := service.pollTailLogs(
			t.Context(),
			tailLogQuery(request, since),
			cursor,
			func(entry *observabilityv1.LogEntry) error { bodies = append(bodies, entry.GetBody()); return nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(bodies) != 1 || bodies[0] != want {
			t.Fatalf("tail records=%v, want [%s]", bodies, want)
		}
	}
	insert(first, "first")
	poll("first")
	insert(midnight.Add(-time.Second), "late previous partition")
	poll("late previous partition")
	if err := client.Conn().Exec(t.Context(), "OPTIMIZE TABLE otel_logs FINAL"); err != nil {
		t.Fatal(err)
	}
	insert(midnight, "late same partition")
	poll("late same partition")
	sent, err := service.pollTailLogs(
		t.Context(),
		tailLogQuery(request, since),
		cursor,
		func(*observabilityv1.LogEntry) error { t.Error("tail repeated a delivered row"); return nil },
	)
	if err != nil || sent != 0 {
		t.Fatalf("unchanged poll=%d, %v", sent, err)
	}
}
