package clickhouse

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const knownTraceID = "0123456789abcdef0123456789abcdef"

func traceRequest(start, end time.Time) *observabilityv1.QueryTracesRequest {
	return &observabilityv1.QueryTracesRequest{
		WorkspaceId: testWorkspaceID,
		StartTime:   timestamppb.New(start),
		EndTime:     timestamppb.New(end),
	}
}

func TestTraceQueryBindsEveryScope(t *testing.T) {
	start := time.Date(2026, time.October, 14, 1, 2, 3, 456, time.UTC)
	req := traceRequest(start, start.Add(time.Hour))
	req.ResourceIds = []string{testResourceID, testWorkerResourceID}
	req.EnvironmentId = testEnvironmentID
	req.TraceId = knownTraceID
	position := logCursor{timestamp: start, blockNumber: 42, blockOffset: 17}
	req.Cursor = position.encode()
	query, args, err := buildTracesQuery(req, testPageSize, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	clauses := []string{"WorkspaceId = ?", "ResourceId IN (?,?)", "EnvironmentId = ?", "TraceId = ?",
		"Timestamp >= fromUnixTimestamp64Nano(?)", "Timestamp <= fromUnixTimestamp64Nano(?)",
		"(Timestamp, _block_number, _block_offset) > (fromUnixTimestamp64Nano(?), ?, ?)",
		"ORDER BY Timestamp ASC, _block_number ASC, _block_offset ASC LIMIT ?", "max_execution_time = 10",
	}
	for _, clause := range clauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q: %s", clause, query)
		}
	}
	if placeholders := strings.Count(query, "?"); placeholders != len(args) {
		t.Errorf("query has %d placeholders for %d args", placeholders, len(args))
	}
	bound := []any{
		testWorkspaceID,
		start.UnixNano(),
		start.Add(time.Hour).UnixNano(),
		testResourceID,
		testWorkerResourceID,
		testEnvironmentID,
		knownTraceID,
		uint64(42),
		uint64(17),
		testPageSize + pageLookahead,
	}
	for _, value := range bound {
		if !slices.Contains(args, value) {
			t.Errorf("args %v do not bind %v", args, value)
		}
	}
	for _, value := range []string{testWorkspaceID, testResourceID, testEnvironmentID, knownTraceID} {
		if strings.Contains(query, value) {
			t.Errorf("query contains unbound value %q", value)
		}
	}
}

func TestTraceQueryRejectsInvalidCursor(t *testing.T) {
	cursors := []string{
		"bad!",
		base64.RawURLEncoding.EncodeToString([]byte("invalid/1/2")),
		base64.RawURLEncoding.EncodeToString([]byte("2026-10-14T00:00:00Z/-1/2")),
	}
	for _, cursor := range cursors {
		req := traceRequest(time.Now().Add(-time.Hour), time.Now())
		req.Cursor = cursor
		_, _, err := buildTracesQuery(req, testPageSize, testQueryTimeout)
		if !errors.Is(err, ErrInvalidTraceCursor) {
			t.Errorf("cursor %q error = %v", cursor, err)
		}
	}
}

func TestTraceSpansScopeAttributionAndConnectivity(t *testing.T) {
	f := newQueryFixture(t)
	const rootID = "1111111111111111"
	const childID = "2222222222222222"
	const duration = uint64(9007199254740993)
	timestamp := f.start.Add(time.Minute + time.Nanosecond)
	batch, prepareErr := f.conn.PrepareBatch(
		t.Context(),
		"INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ParentSpanId, SpanName, "+
			"SpanKind, ServiceName, Duration, StatusCode, StatusMessage, ResourceAttributes, "+
			"SpanAttributes, ScopeName, ScopeVersion)",
	)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	rows := []struct{ workspace, environment, resource, span, parent, service, trace string }{
		{testWorkspaceID, testEnvironmentID, testResourceID, rootID, "", "frontend", knownTraceID},
		{testWorkspaceID, testEnvironmentID, testWorkerResourceID, childID, rootID, "checkout", knownTraceID},
		{testOtherWorkspaceID, testEnvironmentID, testResourceID, "3333333333333333", childID, "foreign", knownTraceID},
		{
			testWorkspaceID,
			testOtherEnvironmentID,
			testResourceID,
			"4444444444444444",
			rootID,
			"other-env",
			knownTraceID,
		},
		{
			testWorkspaceID,
			testEnvironmentID,
			testResourceID,
			"5555555555555555",
			"",
			"other-trace",
			"fedcba9876543210fedcba9876543210",
		},
	}
	for _, row := range rows {
		attrs := tenantAttributes(row.workspace, row.environment, row.resource)
		if err := batch.Append(
			timestamp,
			row.trace,
			row.span,
			row.parent,
			"operation",
			"SPAN_KIND_SERVER",
			row.service,
			duration,
			"STATUS_CODE_ERROR",
			"test failure",
			attrs,
			map[string]string{"request": "known"},
			"fixture",
			"v1",
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, workspace, environment, trace string
		resources                           []string
		want                                []string
	}{
		{
			name:      "workspace",
			workspace: testWorkspaceID,
			want:      []string{rootID, childID, "4444444444444444", "5555555555555555"},
		},
		{
			name:        "known trace and environment",
			workspace:   testWorkspaceID,
			environment: testEnvironmentID,
			trace:       knownTraceID,
			want:        []string{rootID, childID},
		},
		{
			name:        "child without parent resource",
			workspace:   testWorkspaceID,
			environment: testEnvironmentID,
			trace:       knownTraceID,
			resources:   []string{testWorkerResourceID},
			want:        []string{childID},
		},
		{
			name:      "second workspace same trace",
			workspace: testOtherWorkspaceID,
			trace:     knownTraceID,
			want:      []string{"3333333333333333"},
		},
		{name: "unknown workspace", workspace: testUnknownWorkspaceID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := traceRequest(timestamp, timestamp)
			req.WorkspaceId, req.EnvironmentId = tc.workspace, tc.environment
			req.TraceId, req.ResourceIds = tc.trace, tc.resources
			var spans []*observabilityv1.TraceSpan
			for range len(rows) + 1 {
				response, err := QueryTraces(t.Context(), f.conn, req, 1, testQueryTimeout)
				if err != nil {
					t.Fatal(err)
				}
				spans = append(spans, response.GetSpans()...)
				if response.GetNextCursor() == "" {
					break
				}
				if response.GetNextCursor() == req.GetCursor() {
					t.Fatal("cursor did not advance")
				}
				req.Cursor = response.GetNextCursor()
				if err := f.conn.Exec(t.Context(), "OPTIMIZE TABLE otel_traces FINAL"); err != nil {
					t.Fatal(err)
				}
			}
			got := make([]string, 0, len(spans))
			for _, span := range spans {
				got = append(got, span.GetSpanId())
				if span.GetWorkspaceId() != tc.workspace ||
					span.GetResourceAttributes()[workspaceAttributeKey] != tc.workspace {
					t.Errorf("foreign attribution: %v", span)
				}
				if span.GetDurationNanos() != duration || !span.GetStartTime().AsTime().Equal(timestamp) ||
					span.GetName() != "operation" ||
					span.GetKind() != "SPAN_KIND_SERVER" ||
					span.GetStatusCode() != "STATUS_CODE_ERROR" ||
					span.GetStatusMessage() != "test failure" ||
					span.GetScopeName() != "fixture" ||
					span.GetScopeVersion() != "v1" ||
					span.GetSpanAttributes()["request"] != "known" {
					t.Errorf("lost span fields: %v", span)
				}
				if span.GetSpanId() == childID &&
					(span.GetParentSpanId() != rootID || span.GetResourceId() != testWorkerResourceID ||
						span.GetServiceName() != "checkout") {
					t.Errorf("lost connectivity/identity: %v", span)
				}
			}
			slices.Sort(got)
			slices.Sort(tc.want)
			if !slices.Equal(got, tc.want) {
				t.Errorf("spans = %v, want %v", got, tc.want)
			}
		})
	}
	req := traceRequest(timestamp.Add(time.Nanosecond), f.end)
	response, err := QueryTraces(t.Context(), f.conn, req, testFullPage, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetSpans()) != 0 {
		t.Fatal("time bounds included earlier spans")
	}
}

func TestTraceDurationJSONPreservesUint64(t *testing.T) {
	const duration = uint64(18446744073709551615)
	span := &observabilityv1.TraceSpan{DurationNanos: duration}
	data, err := protojson.Marshal(span)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"18446744073709551615"`) {
		t.Fatalf("duration is not a lossless JSON string: %s", data)
	}
	decoded := &observabilityv1.TraceSpan{}
	if err := protojson.Unmarshal(data, decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetDurationNanos() != duration {
		t.Fatalf("duration = %d, want %d", decoded.GetDurationNanos(), duration)
	}
}

func TestTracePagesKeepDuplicateSpansAcrossMerges(t *testing.T) {
	f := newQueryFixture(t)
	const copies = 3
	timestamp := f.start.Add(time.Minute + time.Nanosecond)
	for range copies {
		batch, err := f.conn.PrepareBatch(
			t.Context(),
			"INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ResourceAttributes)",
		)
		if err != nil {
			t.Fatal(err)
		}
		attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
		for range copies {
			if appendErr := batch.Append(timestamp, knownTraceID, "1111111111111111", attrs); appendErr != nil {
				t.Fatal(appendErr)
			}
		}
		if err := batch.Send(); err != nil {
			t.Fatal(err)
		}
	}
	req := traceRequest(timestamp, timestamp)
	req.TraceId = knownTraceID
	count := 0
	for page := range copies * copies {
		response, err := QueryTraces(t.Context(), f.conn, req, testPageSize, testQueryTimeout)
		if err != nil {
			t.Fatal(err)
		}
		count += len(response.GetSpans())
		if page == 0 {
			if err := f.conn.Exec(t.Context(), "OPTIMIZE TABLE otel_traces FINAL"); err != nil {
				t.Fatal(err)
			}
		}
		if response.GetNextCursor() == "" {
			break
		}
		req.Cursor = response.GetNextCursor()
	}
	if count != copies*copies {
		t.Fatalf("returned %d spans, want %d", count, copies*copies)
	}
}
