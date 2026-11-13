package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/clickhouse/migrations"
)

const (
	testLogsTTL           = 48 * time.Hour
	testTelemetryTTL      = time.Hour
	tailLoopCancelDelay   = 100 * time.Millisecond
	tailLoopReturnLimit   = 500 * time.Millisecond
	tailTestEnvironment   = "66666666-7777-8888-9999-000000000000"
	tailTestWorkspace     = "11111111-2222-3333-4444-555555555555"
	tailTestBurstSize     = tailPageSize*2 + 1
	workspaceAttribute    = "loco.workspace.id"
	resourceAttribute     = "loco.resource.id"
	environmentAttribute  = "loco.environment.id"
	logsInsert            = "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes)"
	tracesInsert          = "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ResourceAttributes)"
	wholeTraceSpans       = 4
	resourceIsolationBody = "granted"
)

var errTailTestSend = errors.New("tail receiver disconnected")

// newTestClickHouse migrates a fresh database on LOCO_TEST_CLICKHOUSE_URL and drops it when the
// test ends.
func newTestClickHouse(t *testing.T) *chClient.Client {
	t.Helper()
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	database := "service_" + fmt.Sprintf("%x", uuid.New())
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    testLogsTTL,
		TracesTTL:  testTelemetryTTL,
		MetricsTTL: testTelemetryTTL,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	client, err := chClient.NewClient(dsn, database, observabilityTestConns, observabilityTestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); err != nil {
			t.Error(err)
		}
		client.Close()
	})
	return client
}

func insertLogs(
	t *testing.T,
	client *chClient.Client,
	timestamp time.Time,
	body string,
	attrs map[string]string,
	count int,
) {
	t.Helper()
	batch, err := client.Conn().PrepareBatch(t.Context(), logsInsert)
	if err != nil {
		t.Fatal(err)
	}
	for range count {
		if err := batch.Append(timestamp, body, attrs); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
}

func TestTailLoopReturnsPromptlyWhenCanceledBetweenPolls(t *testing.T) {
	server := NewObservabilityServer(newTestClickHouse(t), nil, ObservabilityConfig{})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(tailLoopCancelDelay, cancel)

	start := time.Now()
	err := server.tailLoop(
		ctx,
		tailLogQuery(&observabilityv1.TailLogsRequest{}, time.Now()),
		func(*observabilityv1.TailLogsResponse) error { return nil },
	)
	if err != nil {
		t.Fatalf("canceled tail = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
}

func TestTailPollDrainsEqualTimestampPagesAndKeepsItsCursor(t *testing.T) {
	client := newTestClickHouse(t)
	server := NewObservabilityServer(client, nil, ObservabilityConfig{})
	timestamp := time.Now().UTC().Add(-time.Minute)
	attrs := map[string]string{workspaceAttribute: tailTestWorkspace, environmentAttribute: tailTestEnvironment}
	insertLogs(t, client, timestamp, "same log", attrs, tailTestBurstSize)
	insertLogs(t, client, timestamp, "build log", map[string]string{workspaceAttribute: tailTestWorkspace}, 1)

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
	poll := func(ctx context.Context) (int, error) {
		return server.pollTailLogs(ctx, tailLogQuery(request, timestamp), cursor, send)
	}
	expect := func(want int) {
		t.Helper()
		sent, err := poll(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if sent != want {
			t.Fatalf("poll delivered %d entries, want %d", sent, want)
		}
	}
	expect(tailTestBurstSize)
	if len(cursor) == 0 {
		t.Fatal("tail has no resume cursor after draining its final page")
	}
	expect(0)
	insertLogs(t, client, timestamp, "same log", attrs, 1)
	expect(1)
	expect(0)
	if sends != tailTestBurstSize+1 {
		t.Fatalf("sent %d entries, want %d", sends, tailTestBurstSize+1)
	}
	insertLogs(t, client, timestamp, "same log", attrs, 1)
	before := maps.Clone(cursor)
	sendErr = errTailTestSend
	if _, err := poll(t.Context()); !errors.Is(err, errTailTestSend) {
		t.Fatalf("send error = %v", err)
	}
	if !maps.Equal(cursor, before) {
		t.Fatal("failed send advanced the cursor")
	}
	sendErr = nil
	expect(1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := poll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled poll error = %v", err)
	}
}

func TestTailPollDeliversLateRecordsAcrossPartitionsAndMerges(t *testing.T) {
	client := newTestClickHouse(t)
	server := NewObservabilityServer(client, nil, ObservabilityConfig{})
	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	since := midnight.Add(-time.Minute)
	attrs := map[string]string{workspaceAttribute: tailTestWorkspace}
	request := &observabilityv1.TailLogsRequest{WorkspaceId: tailTestWorkspace}
	cursor := chClient.TailLogCursor{}
	poll := func(want string) {
		t.Helper()
		var bodies []string
		_, err := server.pollTailLogs(
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
	insertLogs(t, client, time.Now().UTC(), "first", attrs, 1)
	poll("first")
	insertLogs(t, client, midnight.Add(-time.Second), "late previous partition", attrs, 1)
	poll("late previous partition")
	if err := client.Conn().Exec(t.Context(), "OPTIMIZE TABLE otel_logs FINAL"); err != nil {
		t.Fatal(err)
	}
	insertLogs(t, client, midnight, "late same partition", attrs, 1)
	poll("late same partition")
	sent, err := server.pollTailLogs(
		t.Context(),
		tailLogQuery(request, since),
		cursor,
		func(*observabilityv1.LogEntry) error { t.Error("tail repeated a delivered row"); return nil },
	)
	if err != nil || sent != 0 {
		t.Fatalf("unchanged poll=%d, %v", sent, err)
	}
}

func TestResourceReadersSeeOnlyTheirResourceInTheNamedWorkspace(t *testing.T) {
	ch := newTestClickHouse(t)
	f := newObservabilityFixture(t)
	timestamp := time.Now().UTC().Add(-time.Minute)
	for _, row := range []struct {
		resource uuid.UUID
		body     string
	}{
		{f.resource, resourceIsolationBody},
		{f.otherResource, "ungranted"},
	} {
		attrs := map[string]string{workspaceAttribute: f.workspace.String(), resourceAttribute: row.resource.String()}
		insertLogs(t, ch, timestamp, row.body, attrs, 1)
	}
	client := f.client(t, ch)

	for _, tc := range []struct {
		workspace uuid.UUID
		want      []string
	}{
		{f.workspace, []string{resourceIsolationBody}},
		{f.otherWorkspace, []string{}},
	} {
		t.Run(tc.workspace.String(), func(t *testing.T) {
			response, err := client.QueryLogs(callerContext(t, resourceReader), &observabilityv1.QueryLogsRequest{
				WorkspaceId: tc.workspace.String(), ResourceIds: []string{f.resource.String()},
				StartTime: timestamppb.New(timestamp), EndTime: timestamppb.New(timestamp), Limit: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(response.GetEntries()))
			for _, entry := range response.GetEntries() {
				got = append(got, entry.GetBody())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("bodies = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetTraceReadsTheWholeTraceInTheWorkspace(t *testing.T) {
	ch := newTestClickHouse(t)
	f := newObservabilityFixture(t)
	now := time.Now().Add(-time.Minute)
	batch, err := ch.Conn().PrepareBatch(t.Context(), tracesInsert)
	if err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []uuid.UUID{f.workspace, f.otherWorkspace} {
		for i := range wholeTraceSpans {
			attrs := map[string]string{workspaceAttribute: workspace.String()}
			if appendErr := batch.Append(
				now,
				observabilityTestTraceID,
				fmt.Sprintf("%016x", i+1),
				attrs,
			); appendErr != nil {
				t.Fatal(appendErr)
			}
		}
	}
	if sendErr := batch.Send(); sendErr != nil {
		t.Fatal(sendErr)
	}
	client := f.client(t, ch)
	ctx := callerContext(t, workspaceReader)
	scope := &observabilityv1.TraceScope{WorkspaceId: f.workspace.String()}
	trace, err := client.GetTrace(
		ctx,
		&observabilityv1.GetTraceRequest{Scope: scope, TraceId: observabilityTestTraceID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.GetSpans()) != wholeTraceSpans {
		t.Fatalf("GetTrace returned %d spans, want the workspace's %d", len(trace.GetSpans()), wholeTraceSpans)
	}
	list, err := client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
		Scope:     scope,
		StartTime: timestamppb.New(now),
		EndTime:   timestamppb.New(now),
		Limit:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if list.GetTotalCount() != 1 || len(list.GetSummaries()) != 1 ||
		list.GetSummaries()[0].GetSpanCount() != wholeTraceSpans {
		t.Fatalf("ListTraces: %v", list)
	}
}
