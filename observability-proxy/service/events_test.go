package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const (
	eventsTestResource      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	eventsTestOtherResource = "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	eventsTestOtherEnv      = "77777777-8888-9999-0000-111111111111"
	eventsTestForeign       = "99999999-8888-7777-6666-555555555555"
	eventsTestLimit         = 100
	eventsTestBackOff       = "BackOff"
	environmentAttribute    = "loco.environment.id"
	resourceAttribute       = "loco.resource.id"
)

// workspacePermissions allows reads of one workspace only.
type workspacePermissions struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	workspaceID string
}

func (p *workspacePermissions) CheckPermission(
	_ context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	allowed := req.GetEntityType() == tokenv1.EntityType_ENTITY_TYPE_WORKSPACE && req.GetEntityId() == p.workspaceID
	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

type eventRow struct {
	workspace, environment, resource, reason string
	at                                       time.Time
	event                                    bool
}

func newEventsClient(t *testing.T) (observabilityv1connect.ObservabilityProxyServiceClient, func([]eventRow)) {
	t.Helper()
	clickhouse := newTestClickHouse(t)
	controlPlane := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(controlPlane, &workspacePermissions{workspaceID: tailTestWorkspace})
	controlMux := http.NewServeMux()
	connecthttp.Mount(controlMux, controlPlane)
	controlHTTP := httptest.NewServer(controlMux)
	t.Cleanup(controlHTTP.Close)
	validator, err := auth.NewValidator(controlHTTP.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validator.Close)
	cfg := &config.Config{
		DefaultLimit:    eventsTestLimit,
		MaxLimit:        eventsTestLimit,
		MaxTimeRange:    time.Hour,
		MaxTailDuration: time.Minute,
	}
	proxyHTTP := httptest.NewServer(NewObservabilityService(clickhouse, cfg, validator).Handler())
	t.Cleanup(proxyHTTP.Close)
	transport := connecthttp.NewTransport(proxyHTTP.Client(), proxyHTTP.URL)
	client := observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))
	insert := func(rows []eventRow) {
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
				attributes["loco.telemetry.scope"] = "kubernetes-event"
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
	return client, insert
}

func callerContext(t *testing.T) context.Context {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer caller")
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
	client, insert := newEventsClient(t)
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-10 * time.Minute)
	insert([]eventRow{
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
		{eventsTestForeign, tailTestEnvironment, eventsTestResource, "OtherWorkspace", start.Add(time.Minute), true},
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "TooEarly", start.Add(-time.Minute), true},
		{tailTestWorkspace, tailTestEnvironment, eventsTestResource, "a log line", start.Add(time.Minute), false},
	})
	query := func(environment string) (*observabilityv1.QueryEventsResponse, error) {
		return client.QueryEvents(callerContext(t), &observabilityv1.QueryEventsRequest{
			WorkspaceId: tailTestWorkspace, ResourceId: eventsTestResource, EnvironmentId: environment,
			StartTime: timestamppb.New(start), EndTime: timestamppb.New(end),
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
	if got := reasons(resp.GetEvents()); got != "Pulled,BackOff,Scheduled" {
		t.Fatalf("events in every environment = %s, want Pulled,BackOff,Scheduled", got)
	}
}

func TestQueryEventsChecksTheWorkspaceAndIDs(t *testing.T) {
	client, _ := newEventsClient(t)
	now := time.Now()
	query := func(workspace, resource string) error {
		_, err := client.QueryEvents(callerContext(t), &observabilityv1.QueryEventsRequest{
			WorkspaceId: workspace, ResourceId: resource,
			StartTime: timestamppb.New(now.Add(-time.Minute)), EndTime: timestamppb.New(now),
		})
		return err
	}
	if err := query(eventsTestForeign, eventsTestResource); connect.CodeOf(err) != connect.CodePermissionDenied {
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
	client, insert := newEventsClient(t)
	since := time.Now().UTC().Add(-time.Minute)
	insert([]eventRow{
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
	ctx, cancel := context.WithTimeout(callerContext(t), time.Minute)
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
