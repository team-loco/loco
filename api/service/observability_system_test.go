package service

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/systemtenant"
)

const (
	systemTraceAPI        = "0123456789abcdef0123456789abcdef"
	systemTraceController = "fedcba9876543210fedcba9876543210"
	systemMetric          = "process.runtime.go.goroutines"
	systemWorkerCluster   = "worker-1"
	logInfo               = "INFO"
	apiStarted            = "api started"
	apiLostDatabase       = "api lost its database"
	controllerReconciled  = "controller reconciled"
)

func systemResource(component, cluster string) map[string]string {
	return map[string]string{
		workspaceAttribute: systemtenant.WorkspaceID,
		retentionAttribute: "1",
		"service.name":     component,
		"loco.cluster.id":  cluster,
	}
}

// seedSystemTelemetry stores, under the system tenant, a log, a trace and a metric from the API on
// the control plane and from a controller on a worker cluster, and a workspace log that must stay
// out of every system read.
func seedSystemTelemetry(t *testing.T) (client observabilityClient, start, end *timestamppb.Timestamp) {
	t.Helper()
	f := newObservabilityFixture(t)
	at := time.Now().UTC().Add(-time.Minute)
	ch := newTestClickHouse(t)
	logs, err := ch.Conn().PrepareBatch(t.Context(), "INSERT INTO otel_logs "+
		"(Timestamp, Body, SeverityText, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		body, severity string
		resource       map[string]string
	}{
		{apiStarted, logInfo, systemResource("loco-api", systemtenant.ControlPlaneClusterID)},
		{apiLostDatabase, "ERROR", systemResource("loco-api", systemtenant.ControlPlaneClusterID)},
		{controllerReconciled, logInfo, systemResource("loco-controller", systemWorkerCluster)},
		{"workspace log", logInfo, map[string]string{
			workspaceAttribute: f.workspace.String(), retentionAttribute: "1", "service.name": "loco-api",
		}},
	} {
		if err = logs.Append(at, row.body, row.severity, row.resource); err != nil {
			t.Fatal(err)
		}
	}
	if err = logs.Send(); err != nil {
		t.Fatal(err)
	}

	traces, err := ch.Conn().PrepareBatch(t.Context(), "INSERT INTO otel_traces "+
		"(Timestamp, TraceId, SpanId, SpanName, Duration, StatusCode, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	for _, span := range []struct {
		trace, name, status string
		resource            map[string]string
	}{
		{systemTraceAPI, "GET /v1/clusters", "", systemResource("loco-api", systemtenant.ControlPlaneClusterID)},
		{systemTraceController, "reconcile", "Error", systemResource("loco-controller", systemWorkerCluster)},
	} {
		if err = traces.Append(
			at, span.trace, "0000000000000001", span.name, uint64(time.Millisecond), span.status, span.resource,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err = traces.Send(); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		value    float64
		resource map[string]string
	}{
		{10, systemResource("loco-api", systemtenant.ControlPlaneClusterID)},
		{20, systemResource("loco-controller", systemWorkerCluster)},
	} {
		if err = ch.Conn().Exec(
			t.Context(),
			"INSERT INTO otel_metrics_gauge (ResourceAttributes, MetricName, TimeUnix, Value, Flags) "+
				"VALUES (?, ?, ?, ?, 0)",
			row.resource, systemMetric, at, row.value,
		); err != nil {
			t.Fatal(err)
		}
	}
	cfg := observabilityTestConfig
	cfg.Limits.MaxLimit = 100
	client = observabilityTestClient(t, NewObservabilityServer(ch, f.authorizer, f.queries, cfg), f.callers)
	return client, timestamppb.New(at.Add(-time.Second)), timestamppb.New(at.Add(time.Second))
}

func TestSystemReadersQueryLogs(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	logBodies := func(t *testing.T, query string) []string {
		t.Helper()
		response, err := client.QueryLogs(callerContext(t, systemReader), &observabilityv1.QueryLogsRequest{
			WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end, Query: query,
			PageSize: 100, IncludeTotalCount: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if response.GetTotalCount() != int64(len(response.GetEntries())) {
			t.Errorf("total count = %d for %d entries", response.GetTotalCount(), len(response.GetEntries()))
		}
		bodies := make([]string, 0, len(response.GetEntries()))
		for _, entry := range response.GetEntries() {
			bodies = append(bodies, entry.GetBody())
		}
		slices.Sort(bodies)
		return bodies
	}
	for query, want := range map[string][]string{
		"":                                  {apiLostDatabase, apiStarted, controllerReconciled},
		"@component:loco-api":               {apiLostDatabase, apiStarted},
		"@cluster:" + systemWorkerCluster:   {controllerReconciled},
		"@level:error":                      {apiLostDatabase},
		"@component:loco-api -@level:error": {apiStarted},
		"reconciled OR lost":                {apiLostDatabase, controllerReconciled},
		"@component:loco-api AND started":   {apiStarted},
	} {
		if got := logBodies(t, query); !slices.Equal(got, want) {
			t.Errorf("query %q = %v, want %v", query, got, want)
		}
	}
}

func TestSystemReadersQueryLogBuckets(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	response, err := client.QueryLogBuckets(callerContext(t, systemAdmin), &observabilityv1.QueryLogBucketsRequest{
		WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end, IntervalSeconds: 60,
		GroupBy: observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_LEVEL, Query: "@component:loco-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, bucket := range response.GetBuckets() {
		total += bucket.GetCount()
	}
	if total != 2 {
		t.Errorf("buckets count %d logs, want the API's 2: %v", total, response.GetBuckets())
	}
}

func TestSystemReadersTailLogs(t *testing.T) {
	t.Parallel()
	client, start, _ := seedSystemTelemetry(t)
	stream, err := client.TailLogs(callerContext(t, systemReader), &observabilityv1.TailLogsRequest{
		WorkspaceId: systemtenant.WorkspaceID, StartTime: start,
		Query: "@cluster:" + systemWorkerCluster,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		message, err := stream.Receive()
		if err != nil {
			t.Fatalf("tail ended without an entry: %v", err)
		}
		if entry := message.GetEntry(); entry != nil {
			if entry.GetBody() != controllerReconciled {
				t.Fatalf("tail entry = %q, want the controller's", entry.GetBody())
			}
			return
		}
	}
}

func TestSystemReadersQueryMetrics(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	for attributes, want := range map[string]float64{"loco-api": 10, "loco-controller": 20} {
		response, err := client.QueryMetrics(callerContext(t, systemReader), &observabilityv1.QueryMetricsRequest{
			WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end, MetricName: systemMetric,
			IntervalSeconds: 60, Aggregation: "max", Attributes: map[string]string{"component": attributes},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(response.GetSeries()) != 1 || response.GetSeries()[0].GetPoints()[0].GetValue() != want {
			t.Errorf("component %s: series %v, want one at %v", attributes, response.GetSeries(), want)
		}
	}
}

func TestSystemReadersQueryTraces(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	scope := &observabilityv1.TraceScope{WorkspaceId: systemtenant.WorkspaceID}
	ctx := callerContext(t, systemReader)
	for query, want := range map[string][]string{
		"":                           {systemTraceAPI, systemTraceController},
		"@component:loco-controller": {systemTraceController},
		"@cluster:" + systemtenant.ControlPlaneClusterID: {systemTraceAPI},
		"@status:error": {systemTraceController},
	} {
		list, err := client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
			Scope: scope, StartTime: start, EndTime: end, Query: query, PageSize: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(list.GetSummaries()))
		for _, summary := range list.GetSummaries() {
			got = append(got, summary.GetTraceId())
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("ListTraces %q = %v, want %v", query, got, want)
		}
		buckets, err := client.QueryTraceBuckets(ctx, &observabilityv1.QueryTraceBucketsRequest{
			Scope: scope, StartTime: start, EndTime: end, Query: query, IntervalSeconds: 60,
		})
		if err != nil {
			t.Fatal(err)
		}
		if count := buckets.GetTotal().GetTraceCount(); count != uint64(len(want)) {
			t.Errorf("QueryTraceBuckets %q counts %d traces, want %d", query, count, len(want))
		}
	}
	trace, err := client.GetTrace(ctx, &observabilityv1.GetTraceRequest{Scope: scope, TraceId: systemTraceAPI})
	if err != nil || len(trace.GetSpans()) != 1 || trace.GetSpans()[0].GetName() != "GET /v1/clusters" {
		t.Errorf("GetTrace = %v, %v", trace, err)
	}
}

func TestSystemScopeRejectsWorkspaceFilters(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	ctx := callerContext(t, systemReader)
	_, err := client.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
		WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end, Query: "@resource:web",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("@resource in logs: %v, want invalid argument", err)
	}
	_, err = client.ListTraces(ctx, &observabilityv1.ListTracesRequest{
		Scope: &observabilityv1.TraceScope{WorkspaceId: systemtenant.WorkspaceID}, StartTime: start, EndTime: end,
		Query: "@resource:web",
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("@resource in traces: %v, want invalid argument", err)
	}
	_, err = client.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
		WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end, EnvironmentId: tailTestEnvironment,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("environment_id in the system scope: %v, want invalid argument", err)
	}
}

func TestWorkspaceMembersCannotReadTheSystemScope(t *testing.T) {
	t.Parallel()
	client, start, end := seedSystemTelemetry(t)
	for _, caller := range []string{workspaceReader, orgReader, stranger} {
		_, err := client.QueryLogs(callerContext(t, caller), &observabilityv1.QueryLogsRequest{
			WorkspaceId: systemtenant.WorkspaceID, StartTime: start, EndTime: end,
		})
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: %v, want permission denied", caller, err)
		}
	}
}
