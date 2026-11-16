package clickhouse

import (
	"cmp"
	"math"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestServiceGraphCorrelatesScopedCalls(t *testing.T) {
	f := newQueryFixture(t)
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_traces "+
		"(Timestamp, TraceId, SpanId, ParentSpanId, SpanKind, ServiceName, Duration, StatusCode, "+
		"ResourceAttributes, SpanAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	const clientKind, serverKind, failedStatus = "Client", "Server", "Error"
	const firstTrace, secondTrace = "graph-first", "graph-second"
	const clientSpan = "client"
	const foreignSpan = "foreign-span"
	const caller, callee = "graph-caller", "graph-callee"
	const peerAttribute, databasePeer = "peer.service", "database-peer"
	type span struct {
		trace, id, parent, kind, service, workspace, environment, resource, status string
		duration                                                                   time.Duration
		attributes                                                                 map[string]string
		at                                                                         time.Time
	}
	at := f.start.Add(time.Minute)
	add := func(s span) {
		t.Helper()
		if s.at.IsZero() {
			s.at = at
		}
		attrs := tenantAttributes(
			cmp.Or(s.workspace, testWorkspaceID),
			cmp.Or(s.environment, testEnvironmentID),
			cmp.Or(s.resource, testResourceID),
		)
		if appendErr := batch.Append(
			s.at,
			s.trace,
			s.id,
			s.parent,
			s.kind,
			cmp.Or(s.service, "sdk-caller"),
			uint64(s.duration),
			s.status,
			attrs,
			s.attributes,
		); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	for i, trace := range []string{firstTrace, secondTrace, "graph-third"} {
		add(span{trace: trace, id: clientSpan, kind: clientKind, duration: time.Duration(i+1) * 10 * time.Millisecond})
		status := "Ok"
		if i == 0 {
			status = failedStatus
		}
		add(
			span{
				trace:    trace,
				id:       "server",
				parent:   clientSpan,
				kind:     serverKind,
				service:  "sdk-callee",
				resource: testWorkerResourceID,
				status:   status,
				at:       at.Add(30 * time.Minute),
			},
		)
	}
	add(
		span{
			trace:      "external",
			id:         "db",
			kind:       clientKind,
			status:     failedStatus,
			duration:   5 * time.Millisecond,
			attributes: map[string]string{peerAttribute: "  " + databasePeer + "  ", "db.system": "postgresql"},
		},
	)
	add(
		span{
			trace:      "external",
			id:         "queue",
			kind:       clientKind,
			duration:   time.Millisecond,
			attributes: map[string]string{"db.system": "redis"},
		},
	)
	add(span{trace: "unknown", id: clientSpan, kind: clientKind, duration: time.Millisecond})
	add(
		span{
			trace:     firstTrace,
			id:        foreignSpan,
			parent:    clientSpan,
			kind:      serverKind,
			service:   "foreign",
			workspace: testOtherWorkspaceID,
		},
	)
	add(
		span{
			trace:       secondTrace,
			id:          foreignSpan,
			parent:      clientSpan,
			kind:        serverKind,
			service:     "staging",
			environment: testOtherEnvironmentID,
		},
	)
	for _, s := range []span{
		{trace: "foreign", workspace: testOtherWorkspaceID},
		{trace: "staging", environment: testOtherEnvironmentID},
		{trace: "old", at: f.start.Add(-time.Hour)},
	} {
		s.id, s.kind, s.status, s.duration = clientSpan, clientKind, failedStatus, time.Second
		s.attributes = map[string]string{
			peerAttribute: databasePeer,
		}
		add(s)
	}
	if err = batch.Send(); err != nil {
		t.Fatal(err)
	}
	req := &observabilityv1.QueryServiceGraphRequest{
		WorkspaceId:   testWorkspaceID,
		EnvironmentId: testEnvironmentID,
		StartTime:     timestamppb.New(f.start),
		EndTime:       timestamppb.New(f.end),
	}
	response, err := f.client.QueryServiceGraph(
		t.Context(),
		req,
		[]ResourceName{{ID: testResourceID, Name: caller}, {ID: testWorkerResourceID, Name: callee}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEdges()) != 3 {
		t.Fatalf("edges = %v", response)
	}
	want := map[string]struct {
		requests, errors uint64
		p95              float64
	}{callee: {3, 1, 29}, databasePeer: {1, 1, 5}, "redis": {1, 0, 1}}
	for _, edge := range response.GetEdges() {
		expected, ok := want[edge.GetTo()]
		if !ok || edge.GetFrom() != caller || edge.GetRequests() != expected.requests ||
			edge.GetErrors() != expected.errors ||
			math.Abs(edge.GetP95Ms()-expected.p95) > 0.001 {
			t.Fatalf("edge = %v", edge)
		}
	}
	req.WorkspaceId = testUnknownWorkspaceID
	empty, err := f.client.QueryServiceGraph(t.Context(), req, nil)
	if err != nil || len(empty.GetEdges()) != 0 {
		t.Fatalf("unknown workspace = %v (%v)", empty, err)
	}
}
