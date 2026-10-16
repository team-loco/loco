package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/migrations"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var errTailTestSend = errors.New("tail receiver disconnected")

const (
	tailTestEnvironment  = "66666666-7777-8888-9999-000000000000"
	tailTestWorkspace    = "11111111-2222-3333-4444-555555555555"
	tailTestQueryTimeout = 10
	tailTestBurstSize    = tailPageSize*2 + 1
)

func TestTailPollDrainsEqualTimestampPagesAndKeepsItsCursor(t *testing.T) {
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	database := "tail_" + fmt.Sprintf("%x", uuid.New())
	migration := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    time.Hour,
		TracesTTL:  time.Hour,
		MetricsTTL: time.Hour,
	}
	if err := migrations.Up(t.Context(), migration); err != nil {
		t.Fatal(err)
	}
	client, err := chClient.NewClient(dsn, database, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer func() {
		if err := client.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); err != nil {
			t.Error(err)
		}
	}()
	service := NewObservabilityService(client, &config.Config{QueryTimeout: tailTestQueryTimeout}, nil)
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
	cursor := ""
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
		sent, err := service.pollTailLogs(t.Context(), request, timestamp, time.Now(), &cursor, send)
		if err != nil {
			t.Fatal(err)
		}
		if sent != want {
			t.Fatalf("poll delivered %d entries, want %d", sent, want)
		}
	}
	poll(tailTestBurstSize)
	if cursor == "" {
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
	before := cursor
	sendErr = errTailTestSend
	if _, err := service.pollTailLogs(
		t.Context(),
		request,
		timestamp,
		time.Now(),
		&cursor,
		send,
	); !errors.Is(
		err,
		errTailTestSend,
	) {
		t.Fatalf("send error = %v", err)
	}
	if cursor != before {
		t.Fatal("failed send advanced the cursor")
	}
	sendErr = nil
	poll(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.pollTailLogs(
		ctx,
		request,
		timestamp,
		time.Now(),
		&cursor,
		send,
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("canceled poll error = %v", err)
	}
}
