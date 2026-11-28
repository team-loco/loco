package service

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	genDb "github.com/team-loco/loco/api/gen/db"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/clickhouse/clickhousetest"
	"github.com/team-loco/loco/internal/clickhouse/migrations"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testTelemetryRegionAttribute = "loco.region"

func TestPathBreakdownLimitReturnsAnActionableError(t *testing.T) {
	t.Parallel()
	store := newObservabilityQueryStore(t)
	f := newObservabilityFixture(t)
	config := observabilityTestConfig
	config.RequestMetricsLimits.MaxGroups = 1
	client := observabilityTestClient(t, NewObservabilityServer(store, f.authorizer, f.queries, config), f.callers)
	start := time.Now().Add(-time.Minute).Truncate(time.Minute)
	attrs := map[string]string{
		"loco.workspace.id":                         f.workspace.String(),
		"loco.resource.id":                          f.resource.String(),
		"loco.environment.id":                       testEnvironmentIDForRequestMetrics,
		"loco.retention.days":                       "1",
		testTelemetryRegionAttribute:                testRegion,
		"gateway.envoyproxy.io/owning-gateway-name": "loco",
	}
	for _, path := range []string{"/first?secret=discarded", "/second"} {
		logs := map[string]string{"path": path, "duration": "12", "response_code": "200", "upstream_cluster": "route"}
		if err := store.Conn().
			Exec(t.Context(), "INSERT INTO otel_logs (Timestamp,ResourceAttributes,LogAttributes) "+
				"VALUES (?,?,?)", start, attrs, logs); err != nil {
			t.Fatal(err)
		}
	}
	request := &observabilityv1.QueryRequestMetricsRequest{
		WorkspaceId:     f.workspace.String(),
		ResourceId:      f.resource.String(),
		EnvironmentId:   testEnvironmentIDForRequestMetrics,
		StartTime:       timestamppb.New(start),
		EndTime:         timestamppb.New(start.Add(time.Minute)),
		IntervalSeconds: 60,
	}
	_, err := client.QueryRequestMetrics(callerContext(t, workspaceReader), request)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "Shorten the time range or filter paths") {
		t.Fatalf("broad query error=%v", err)
	}
	t.Logf("Synthetic gateway access logs; QueryRequestMetrics(path=empty, group limit=1) => %v", err)
	request.Path = "/first"
	response, err := client.QueryRequestMetrics(callerContext(t, workspaceReader), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetBuckets()) != 1 {
		t.Fatalf("filtered response=%v", response)
	}
	if response.GetBuckets()[0].GetPath() != "/first" || response.GetBuckets()[0].GetRequests() != 1 {
		t.Fatalf("filtered response=%v", response)
	}
	t.Logf("QueryRequestMetrics(path=/first) => %v", response)
}

func newObservabilityQueryStore(t *testing.T) *chClient.Client {
	t.Helper()
	database := clickhousetest.New(t, "loco_request_metrics_")
	if err := migrations.Up(t.Context(), migrations.Config{DSN: database.DSN, Database: database.Name}); err != nil {
		t.Fatal(err)
	}
	store, err := chClient.NewClient(database.DSN, database.Name, 2, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestReadableMetricScopeIncludesOnlyGrantedResources(t *testing.T) {
	t.Parallel()
	store := newObservabilityQueryStore(t)
	f := newObservabilityFixture(t)
	f.callers[resourceReader] = append(
		f.callers[resourceReader],
		genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: f.foreignResource, Scope: genDb.ScopeRead},
	)
	client := f.client(t, store)
	at := time.Now().Add(-time.Minute).Truncate(time.Minute)
	for _, sample := range []struct {
		workspace, resource string
		value               float64
	}{
		{f.workspace.String(), f.resource.String(), 2},
		{f.workspace.String(), f.otherResource.String(), 100},
		{f.otherWorkspace.String(), f.foreignResource.String(), 1000},
	} {
		attrs := map[string]string{
			"loco.workspace.id":          sample.workspace,
			"loco.resource.id":           sample.resource,
			"loco.environment.id":        testEnvironmentIDForRequestMetrics,
			"loco.retention.days":        "1",
			testTelemetryRegionAttribute: testRegion,
		}
		if err := store.Conn().
			Exec(t.Context(), "INSERT INTO otel_metrics_gauge (ResourceAttributes,MetricName,TimeUnix,Value) "+
				"VALUES (?,?,?,?)", attrs, "test.usage", at, sample.value); err != nil {
			t.Fatal(err)
		}
	}
	req := &observabilityv1.QueryMetricsRequest{
		WorkspaceId:       f.workspace.String(),
		EnvironmentId:     testEnvironmentIDForRequestMetrics,
		StartTime:         timestamppb.New(at),
		EndTime:           timestamppb.New(at.Add(time.Minute)),
		MetricName:        "test.usage",
		IntervalSeconds:   60,
		Aggregation:       "avg",
		ByRegion:          true,
		ReadableResources: true,
	}
	response, err := client.QueryMetrics(callerContext(t, resourceReader), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetSeries()) != 1 {
		t.Fatalf("granted metrics=%v", response)
	}
	points := response.GetSeries()[0].GetPoints()
	if len(points) != 1 {
		t.Fatalf("granted points=%v", points)
	}
	if response.GetSeries()[0].GetResourceId() != f.resource.String() || points[0].GetValue() != 2 {
		t.Fatalf("granted metrics=%v", response)
	}
	t.Logf("Synthetic gauges; QueryMetrics(readable_resources=true, resource-only caller) => %v", response)
	response, err = client.QueryMetrics(callerContext(t, workspaceReader), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetSeries()) != 2 {
		t.Fatalf("workspace metrics=%v", response)
	}
	if _, err = client.QueryMetrics(
		callerContext(t, stranger),
		req,
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("stranger error=%v", err)
	}
}
