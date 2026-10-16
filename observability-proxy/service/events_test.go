package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/migrations"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const eventHTTPUIDKey = "uid"
const eventHTTPKindKey = "kind"
const eventHTTPNamespaceKey = "namespace"

const eventHTTPApplicationUID = "app"

const eventHTTPAPIVersionKey = "apiVersion"

const eventHTTPResource = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
const eventHTTPRegion = "test-region"

func TestQueryEventsHTTPRequiresWorkspaceReadBeforeStorage(t *testing.T) {
	client := traceTestClient(t, nil)
	for _, tc := range []struct {
		name, token, workspace, region string
		missingBounds                  bool
		code                           connect.Code
	}{
		{name: "missing token", workspace: tailTestWorkspace, region: eventHTTPRegion, code: connect.CodeUnauthenticated},
		{
			name:      "unknown token",
			token:     "unknown-events-reader",
			workspace: tailTestWorkspace,
			region:    eventHTTPRegion,
			code:      connect.CodePermissionDenied,
		},
		{
			name:      "resource only",
			token:     "resource-reader",
			workspace: tailTestWorkspace,
			region:    eventHTTPRegion,
			code:      connect.CodePermissionDenied,
		},
		{
			name:      "wrong workspace",
			token:     traceTestReader,
			workspace: traceTestOtherWorkspace,
			region:    eventHTTPRegion,
			code:      connect.CodePermissionDenied,
		},
		{
			name:      "invalid workspace",
			token:     traceTestReader,
			workspace: "invalid",
			region:    eventHTTPRegion,
			code:      connect.CodeInvalidArgument,
		},
		{name: "missing region", token: traceTestReader, workspace: tailTestWorkspace, code: connect.CodeInvalidArgument},
		{
			name:          "missing bounds",
			token:         traceTestReader,
			workspace:     tailTestWorkspace,
			region:        eventHTTPRegion,
			missingBounds: true,
			code:          connect.CodeInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, info := connect.NewClientContext(t.Context())
			if tc.token != "" {
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
			}
			req := &observabilityv1.QueryEventsRequest{
				WorkspaceId: tc.workspace,
				Region:      tc.region,
				StartTime:   timestamppb.New(time.Now().Add(-time.Minute)),
				EndTime:     timestamppb.Now(),
			}
			if tc.missingBounds {
				req.StartTime = nil
			}
			_, err := client.QueryEvents(ctx, req)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code=%v,want %v: %v", connect.CodeOf(err), tc.code, err)
			}
			if strings.Contains(err.Error(), "127.0.0.1") {
				t.Fatalf("storage details leaked: %v", err)
			}
		})
	}
}

func TestQueryEventsHTTPReadsDeletedResourceHistoryWithoutResourceAPI(t *testing.T) {
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	id := uuid.New()
	database := "event_http_" + fmt.Sprintf("%x", id)
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    time.Hour,
		TracesTTL:  time.Hour,
		MetricsTTL: time.Hour,
		EventsTTL:  time.Hour,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	ch, err := chClient.NewClient(dsn, database, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dropErr := ch.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); dropErr != nil {
			t.Error(dropErr)
		}
		ch.Close()
	})
	now := time.Now().UTC()
	observed := now.Add(-time.Minute)
	namespace := "ws-" + tailTestWorkspace
	rows := []struct {
		table string
		body  map[string]any
	}{
		{
			"otel_workload_owners",
			map[string]any{
				eventHTTPUIDKey:        eventHTTPApplicationUID,
				eventHTTPKindKey:       "Application",
				eventHTTPAPIVersionKey: "infra.loco.io/v1alpha1",
				eventHTTPNamespaceKey:  "loco-system",
				"applicationUid":       eventHTTPApplicationUID,
				"organizationId":       eventHTTPResource,
				"workspaceId":          tailTestWorkspace,
				"resourceId":           eventHTTPResource,
				"resourceName":         "deleted-cart",
			},
		},
		{
			"otel_workload_owners",
			map[string]any{
				eventHTTPUIDKey:        "deployment",
				eventHTTPKindKey:       "Deployment",
				eventHTTPAPIVersionKey: "apps/v1",
				eventHTTPNamespaceKey:  namespace,
				"applicationUid":       eventHTTPApplicationUID,
				"environmentId":        eventHTTPResource,
				"deletionTimestamp":    observed.Format(time.RFC3339Nano),
			},
		},
		{
			"otel_workload_events",
			map[string]any{
				eventHTTPUIDKey:        "native-event",
				eventHTTPKindKey:       "Event",
				eventHTTPAPIVersionKey: "events.k8s.io/v1",
				eventHTTPNamespaceKey:  namespace,
				"regarding": map[string]string{
					eventHTTPUIDKey:        "deployment",
					eventHTTPKindKey:       "Deployment",
					eventHTTPAPIVersionKey: "apps/v1",
					eventHTTPNamespaceKey:  namespace,
				},
				"type":              "Warning",
				"reason":            "UnknownPrivateReason",
				"note":              "PRIVATE-OBJECT-AND-RAW-ERROR",
				"eventTime":         observed.Format(time.RFC3339Nano),
				"creationTimestamp": observed.Format(time.RFC3339Nano),
			},
		},
	}
	for _, row := range rows {
		body, marshalErr := json.Marshal(row.body)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		batch, prepareErr := ch.Conn().
			PrepareBatch(t.Context(), "INSERT INTO "+row.table+" (Timestamp, Body, ResourceAttributes)")
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if appendErr := batch.Append(
			observed,
			string(body),
			map[string]string{"k8s.cluster.uid": "cluster", "loco.region": eventHTTPRegion},
		); appendErr != nil {
			t.Fatal(appendErr)
		}
		if sendErr := batch.Send(); sendErr != nil {
			t.Fatal(sendErr)
		}
	}
	client := traceTestClient(t, ch)
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
	req := &observabilityv1.QueryEventsRequest{
		WorkspaceId:   tailTestWorkspace,
		ResourceIds:   []string{eventHTTPResource},
		EnvironmentId: eventHTTPResource,
		Region:        eventHTTPRegion,
		StartTime:     timestamppb.New(observed.Add(-time.Second)),
		EndTime:       timestamppb.New(now),
	}
	response, err := client.QueryEvents(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetEvents()) != 1 {
		t.Fatalf("retained resource history unavailable: %v", response)
	}
	event := response.GetEvents()[0]
	if event.GetResourceName() != "deleted-cart" || event.GetOccurrences() != 1 ||
		event.GetSummary() != "Application reported a warning." {
		t.Fatalf("wrong safe retained event: %v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "deployment") ||
		strings.Contains(string(encoded), "native-event") {
		t.Fatalf("implementation details leaked: %s", encoded)
	}
	req.Cursor = "invalid"
	_, err = client.QueryEvents(ctx, req)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid cursor code=%v: %v", connect.CodeOf(err), err)
	}
}

func TestQueryEventsHTTPEnforcesDeclaredRequestBounds(t *testing.T) {
	client := traceTestClient(t, nil)
	for _, tc := range []struct {
		name   string
		change func(*observabilityv1.QueryEventsRequest)
	}{
		{"resources", func(req *observabilityv1.QueryEventsRequest) {
			req.ResourceIds = make([]string, 101)
			for index := range req.GetResourceIds() {
				req.ResourceIds[index] = eventHTTPResource
			}
		}},
		{"resource UUID", func(req *observabilityv1.QueryEventsRequest) { req.ResourceIds = []string{"not-a-uuid"} }},
		{"environment UUID", func(req *observabilityv1.QueryEventsRequest) { req.EnvironmentId = "not-a-uuid" }},
		{"unknown severity", func(req *observabilityv1.QueryEventsRequest) {
			req.Severities = []observabilityv1.WorkloadEventSeverity{99}
		}},
		{"zero severity", func(req *observabilityv1.QueryEventsRequest) {
			req.Severities = []observabilityv1.WorkloadEventSeverity{0}
		}},
		{"search", func(req *observabilityv1.QueryEventsRequest) { req.Search = strings.Repeat("a", 257) }},
		{"cursor", func(req *observabilityv1.QueryEventsRequest) { req.Cursor = strings.Repeat("a", 4097) }},
		{"region", func(req *observabilityv1.QueryEventsRequest) { req.Region = strings.Repeat("a", 129) }},
		{"whitespace region", func(req *observabilityv1.QueryEventsRequest) { req.Region = " " }},
		{"negative limit", func(req *observabilityv1.QueryEventsRequest) { req.Limit = -1 }},
		{"limit", func(req *observabilityv1.QueryEventsRequest) { req.Limit = 10001 }},
		{"timestamp overflow", func(req *observabilityv1.QueryEventsRequest) {
			req.StartTime = &timestamppb.Timestamp{Seconds: 253402300799}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, info := connect.NewClientContext(t.Context())
			info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
			req := &observabilityv1.QueryEventsRequest{
				WorkspaceId: tailTestWorkspace,
				Region:      eventHTTPRegion,
				StartTime:   timestamppb.New(time.Now().Add(-time.Minute)),
				EndTime:     timestamppb.Now(),
			}
			tc.change(req)
			_, err := client.QueryEvents(ctx, req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid field reached storage: %v", err)
			}
		})
	}
}
