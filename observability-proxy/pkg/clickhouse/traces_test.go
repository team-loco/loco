package clickhouse

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"strings"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const knownTraceID = "0123456789abcdef0123456789abcdef"

func TestGetTraceReturnsAllScopedSpans(t *testing.T) {
	f, list := newTraceSummaryFixture(t)
	// A trace can outlive the list window and the configured list page size.
	batch, prepareErr := f.conn.PrepareBatch(
		t.Context(),
		"INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ResourceAttributes)",
	)
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	for i := range 5 {
		if err := batch.Append(
			f.start.Add(time.Duration(i)*time.Hour),
			fmt.Sprintf("%032x", 1),
			fmt.Sprintf("late-%d", i),
			tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, workspace, environment string
		resources                    []string
		count                        int
	}{
		{"workspace", testWorkspaceID, testEnvironmentID, nil, 7},
		{"resource", testWorkspaceID, testEnvironmentID, []string{testWorkerResourceID}, 1},
		{"separate workspace", testOtherWorkspaceID, testEnvironmentID, nil, 1},
		{"other environment", testWorkspaceID, testOtherEnvironmentID, nil, 1},
		{"unknown workspace", testUnknownWorkspaceID, testEnvironmentID, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &observabilityv1.GetTraceRequest{
				Scope: &observabilityv1.TraceScope{
					WorkspaceId:   tc.workspace,
					EnvironmentId: tc.environment,
					ResourceIds:   tc.resources,
				},
				TraceId: fmt.Sprintf("%032x", 1),
			}
			response, err := f.client.GetTrace(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(response.GetSpans()) != tc.count {
				t.Fatalf("spans: %v", response)
			}
			for _, span := range response.GetSpans() {
				if span.GetWorkspaceId() != tc.workspace || span.GetEnvironmentId() != tc.environment ||
					span.GetTraceId() != req.GetTraceId() {
					t.Fatalf("scope: %v", span)
				}
				if len(tc.resources) > 0 && !slices.Contains(tc.resources, span.GetResourceId()) {
					t.Fatalf("resource: %v", span)
				}
			}
		})
	}
	list.Limit = 1
	summaries, err := f.client.ListTraces(t.Context(), list, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries.GetSummaries()) != 1 {
		t.Fatalf("list page: %v", summaries)
	}
}
func TestTraceSpansCarryEvents(t *testing.T) {
	f := newQueryFixture(t)
	timestamp := f.start.Add(time.Minute + time.Nanosecond)
	exceptionTime := timestamp.Add(time.Millisecond + time.Nanosecond)
	exception := map[string]string{
		"exception.type":       "ValueError",
		"exception.message":    "bad input",
		"exception.stacktrace": "Traceback (most recent call last):\n  File \"app.py\", line 1",
	}
	batch, err := f.conn.PrepareBatch(
		t.Context(),
		"INSERT INTO otel_traces (Timestamp, TraceId, SpanId, ResourceAttributes, "+
			"Events.Timestamp, Events.Name, Events.Attributes)",
	)
	if err != nil {
		t.Fatal(err)
	}
	attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	if err = batch.Append(
		timestamp, knownTraceID, "1111111111111111", attrs,
		[]time.Time{timestamp, exceptionTime},
		[]string{"retry", "exception"},
		[]map[string]string{{}, exception},
	); err != nil {
		t.Fatal(err)
	}
	if err = batch.Send(); err != nil {
		t.Fatal(err)
	}
	response, err := f.client.GetTrace(
		t.Context(),
		&observabilityv1.GetTraceRequest{
			Scope:   &observabilityv1.TraceScope{WorkspaceId: testWorkspaceID},
			TraceId: knownTraceID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetSpans()) != 1 {
		t.Fatalf("spans = %v, want one", response.GetSpans())
	}
	events := response.GetSpans()[0].GetEvents()
	if len(events) != 2 ||
		events[0].GetName() != "retry" || !events[0].GetTimestamp().AsTime().Equal(timestamp) ||
		len(events[0].GetAttributes()) != 0 ||
		events[1].GetName() != "exception" || !events[1].GetTimestamp().AsTime().Equal(exceptionTime) ||
		!maps.Equal(events[1].GetAttributes(), exception) {
		t.Errorf("events = %v", events)
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
