package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const (
	eventsTestResource      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	eventsTestOtherResource = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	eventsTestOtherEnv      = "77777777-8888-9999-0000-111111111111"
	eventsTestLimit         = 2
	eventsTestBackOff       = "BackOff"
	environmentAttribute    = "loco.environment.id"
	resourceAttribute       = "loco.resource.id"
)

type eventRow struct {
	workspace, environment, resource, reason string
	at                                       time.Time
	event                                    bool
}

func insertEventRows(t *testing.T, clickhouse *chClient.Client, rows []eventRow) {
	t.Helper()
	batch, err := clickhouse.Conn().
		PrepareBatch(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		attributes := map[string]string{
			counterHTTPWorkspaceAttribute: row.workspace,
			environmentAttribute:          row.environment,
			resourceAttribute:             row.resource,
		}
		body := row.reason
		if row.event {
			attributes[chClient.TelemetryScopeAttribute] = chClient.KubernetesEventScope
			body = fmt.Sprintf(`{"type":"ADDED","object":{"reason":%q,"note":"note of %s","type":"Warning",`+
				`"regarding":{"kind":"Pod","name":"resource-%s-5d9c7-x2x8q"}}}`, row.reason, row.reason, row.resource)
		}
		if err := batch.Append(row.at, body, attributes); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
}

func readerContext(t *testing.T) context.Context {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
	return ctx
}

func reasons(events []*observabilityv1.Event) string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.GetReason())
	}
	return strings.Join(names, ",")
}

func TestQueryEventsReturnsOneResourcesEventsInRange(t *testing.T) {
	clickhouse := newTestClickHouse(t)
	client := traceTestClient(t, clickhouse)
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-10 * time.Minute)
	insertEventRows(t, clickhouse, []eventRow{
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "Scheduled", start.Add(time.Minute), true},
		{
			tailTestWorkspace,
			tailTestEnvironment,
			eventsTestResource,
			eventsTestBackOff,
			start.Add(2 * time.Minute),
			true,
		},
		{tailTestWorkspace, eventsTestOtherEnv, eventsTestResource, "Pulled", start.Add(3 * time.Minute), true},
		{
			tailTestWorkspace,
			tailTestEnvironment,
			eventsTestOtherResource,
			"OtherResource",
			start.Add(time.Minute),
			true,
		},
		{
			traceTestOtherWorkspace,
			tailTestEnvironment,
			eventsTestResource,
			"OtherWorkspace",
			start.Add(time.Minute),
			true,
		},
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "TooEarly", start.Add(-time.Minute), true},
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "a log line", start.Add(time.Minute), false},
	})
	query := func(environment string) (*observabilityv1.QueryEventsResponse, error) {
		return client.QueryEvents(readerContext(t), &observabilityv1.QueryEventsRequest{
			WorkspaceId: tailTestWorkspace, ResourceId: eventsTestResource, EnvironmentId: environment,
			StartTime: timestamppb.New(start), EndTime: timestamppb.New(end), Limit: eventsTestLimit,
		})
	}

	resp, err := query(tailTestEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if got := reasons(resp.GetEvents()); got != "BackOff,Scheduled" {
		t.Fatalf("events in the environment = %s, want BackOff,Scheduled", got)
	}
	backOff := resp.GetEvents()[0]
	want := fmt.Sprintf("resource-%s-5d9c7-x2x8q", eventsTestResource)
	if backOff.GetMessage() != "note of BackOff" || backOff.GetType() != "Warning" ||
		backOff.GetObjectKind() != "Pod" || backOff.GetObjectName() != want ||
		!backOff.GetTimestamp().AsTime().Equal(start.Add(2*time.Minute)) {
		t.Fatalf("event = %v", backOff)
	}

	resp, err = query("")
	if err != nil {
		t.Fatal(err)
	}
	if got := reasons(resp.GetEvents()); got != "Pulled,BackOff" {
		t.Fatalf("newest %d events in every environment = %s, want Pulled,BackOff", eventsTestLimit, got)
	}
}

func TestQueryEventsChecksTheWorkspaceAndIDs(t *testing.T) {
	client := traceTestClient(t, unavailableClickHouse(t))
	now := time.Now()
	query := func(workspace, resource string) error {
		_, err := client.QueryEvents(readerContext(t), &observabilityv1.QueryEventsRequest{
			WorkspaceId: workspace, ResourceId: resource,
			StartTime: timestamppb.New(now.Add(-time.Minute)), EndTime: timestamppb.New(now),
		})
		return err
	}
	if err := query(traceTestOtherWorkspace, eventsTestResource); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("another workspace = %v, want permission denied", err)
	}
	if err := query(
		tailTestWorkspace,
		strings.ToUpper(eventsTestResource),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("uppercase resource = %v, want invalid argument", err)
	}
}

func TestTailEventsStreamsOnlyTheResourcesEvents(t *testing.T) {
	clickhouse := newTestClickHouse(t)
	client := traceTestClient(t, clickhouse)
	since := time.Now().UTC().Add(-time.Minute)
	insertEventRows(t, clickhouse, []eventRow{
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "a log line", since.Add(time.Second), false},
		{
			tailTestWorkspace,
			tailTestEnvironment,
			eventsTestOtherResource,
			"OtherResource",
			since.Add(time.Second),
			true,
		},
		{
			tailTestWorkspace,
			tailTestEnvironment,
			eventsTestResource,
			eventsTestBackOff,
			since.Add(2 * time.Second),
			true,
		},
	})
	ctx, cancel := context.WithTimeout(readerContext(t), time.Minute)
	defer cancel()
	stream, err := client.TailEvents(ctx, &observabilityv1.TailEventsRequest{
		WorkspaceId: tailTestWorkspace, ResourceId: eventsTestResource, Since: timestamppb.New(since),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if first.GetEvent().GetReason() != eventsTestBackOff {
		t.Fatalf("first message = %v, want the BackOff event", first)
	}
	next, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if next.GetEvent() != nil {
		t.Fatalf("streamed %v after the only matching event", next)
	}
}
