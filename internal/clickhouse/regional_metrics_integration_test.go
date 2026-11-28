package clickhouse

import (
	"context"
	"errors"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRegionalUsageSumsPodsBeforeCombiningResources(t *testing.T) {
	f := newQueryFixture(t)
	const metric = "regional.cpu.usage"
	f.insertPodSamples(t, metric, false,
		podSample{testWorkspaceID, testResourceID, podA, f.start, 2},
		podSample{testWorkspaceID, testResourceID, podA, f.start.Add(10 * time.Second), 4},
		podSample{testWorkspaceID, testResourceID, podB, f.start, 5},
		podSample{testWorkspaceID, testWorkerResourceID, podA, f.start, 7},
		podSample{testOtherWorkspaceID, testResourceID, podA, f.start, 1000},
	)
	attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	attrs[regionAttribute], attrs[podAttribute] = testWorkerRegion, podA
	if err := f.conn.Exec(
		t.Context(),
		"INSERT INTO otel_metrics_gauge (ResourceAttributes,MetricName,TimeUnix,Value) VALUES (?,?,?,?)",
		attrs,
		metric,
		f.start,
		11,
	); err != nil {
		t.Fatal(err)
	}
	req := metricsRequest(f.start, f.end)
	req.ResourceIds = nil
	req.Region = ""
	req.MetricName = metric
	req.ByRegion, req.SumPods = true, true
	check := func(want map[string]float64) {
		t.Helper()
		got, err := f.client.QueryMetrics(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, s := range got {
			if len(s.GetPoints()) != 1 {
				t.Fatalf("points=%v", s)
			}
			values[s.GetResourceId()+"/"+s.GetLabels()[regionAttribute]] = s.GetPoints()[0].GetValue()
		}
		assertPodValues(t, values, want)
	}
	check(
		map[string]float64{
			testResourceID + "/" + testRegion:       8,
			testWorkerResourceID + "/" + testRegion: 7,
			testResourceID + "/" + testWorkerRegion: 11,
		},
	)
	req.CombineResources = true
	check(map[string]float64{"/" + testRegion: 15, "/" + testWorkerRegion: 11})
	req.Regions = []string{testWorkerRegion}
	check(map[string]float64{"/" + testWorkerRegion: 11})
	req.SumPods = false
	check(map[string]float64{"/" + testWorkerRegion: 11})
}

func TestRequestMetricsKeepsRegionsAndLiteralPathsWithinScope(t *testing.T) {
	f := newQueryFixture(t)
	insert := func(workspace, env, resource, region, path, duration, status string, gateway bool) {
		t.Helper()
		attrs := tenantAttributes(workspace, env, resource)
		attrs[regionAttribute] = region
		if gateway {
			attrs["gateway.envoyproxy.io/owning-gateway-name"] = "loco"
		}
		logs := map[string]string{
			"path":             path,
			"duration":         duration,
			"response_code":    status,
			"upstream_cluster": "gateway-upstream",
		}
		if err := f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_logs (Timestamp,ResourceAttributes,LogAttributes) VALUES (?,?,?)",
			f.start,
			attrs,
			logs,
		); err != nil {
			t.Fatal(err)
		}
	}
	insert(
		testWorkspaceID,
		testEnvironmentID,
		testResourceID,
		testRegion,
		"/users/123?token=private",
		"10",
		"200",
		true,
	)
	insert(testWorkspaceID, testEnvironmentID, testResourceID, testRegion, "/users/123?token=other", "30", "503", true)
	insert(testWorkspaceID, testEnvironmentID, testResourceID, testWorkerRegion, "/users/456", "90", "200", true)
	insert(testOtherWorkspaceID, testEnvironmentID, testResourceID, testRegion, "/users/123", "1000", "500", true)
	insert(testWorkspaceID, testOtherEnvironmentID, testResourceID, testRegion, "/users/123", "1000", "500", true)
	insert(testWorkspaceID, testEnvironmentID, testWorkerResourceID, testRegion, "/users/123", "1000", "500", true)
	insert(testWorkspaceID, testEnvironmentID, testResourceID, testRegion, "/users/123", "1000", "500", false)
	req := &observabilityv1.QueryRequestMetricsRequest{
		WorkspaceId:     testWorkspaceID,
		EnvironmentId:   testEnvironmentID,
		ResourceId:      testResourceID,
		StartTime:       timestamppb.New(f.start),
		EndTime:         timestamppb.New(f.end),
		IntervalSeconds: 60,
	}
	got, err := f.client.QueryRequestMetrics(
		t.Context(),
		req,
		RequestMetricsLimits{MaxGroups: 100, MaxMemoryBytes: 256 * 1024 * 1024, MaxRowsRead: 10000000},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetBuckets()) != 2 {
		t.Fatalf("buckets=%v", got)
	}
	for _, b := range got.GetBuckets() {
		if b.GetRegion() == testRegion {
			if b.GetPath() != "/users/123" || b.GetRequests() != 2 || b.GetServerErrors() != 1 {
				t.Fatalf("bucket=%v", b)
			}
			if b.GetP50Millis() < 10 || b.GetP50Millis() > 30 || b.GetP95Millis() != 30 {
				t.Fatalf("bucket=%v", b)
			}
		} else if b.GetRegion() != testWorkerRegion {
			t.Fatalf("unexpected region=%v", b)
		}
	}
	req.Regions = []string{testWorkerRegion}
	req.Path = "456"
	got, err = f.client.QueryRequestMetrics(
		t.Context(),
		req,
		RequestMetricsLimits{MaxGroups: 100, MaxMemoryBytes: 256 * 1024 * 1024, MaxRowsRead: 10000000},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetBuckets()) != 1 || got.GetBuckets()[0].GetPath() != "/users/456" {
		t.Fatalf("filtered=%v", got)
	}
	req.Regions = nil
	req.Path = ""
	if _, err = f.client.QueryRequestMetrics(
		t.Context(),
		req,
		RequestMetricsLimits{MaxGroups: 1, MaxMemoryBytes: 256 * 1024 * 1024, MaxRowsRead: 10000000},
	); !errors.Is(
		err,
		ErrRequestMetricsTooBroad,
	) {
		t.Fatalf("error=%v, expected full path/region/time grouping limit", err)
	}
}

func TestCombinedGatewayMetricsMergeCountsWithinEachRegion(t *testing.T) {
	f := newQueryFixture(t)
	for _, sample := range []struct {
		resource, region string
		counts           []uint64
		requests         float64
	}{
		{testResourceID, testRegion, []uint64{100, 0, 0}, 100},
		{testWorkerResourceID, testRegion, []uint64{0, 1, 0}, 1},
		{testResourceID, testWorkerRegion, []uint64{0, 1, 0}, 1},
	} {
		attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, sample.resource)
		attrs[regionAttribute] = sample.region
		at := f.start.Add(time.Minute)
		if err := f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_metrics_histogram (ResourceAttributes,MetricName,StartTimeUnix,TimeUnix,"+
				"BucketCounts,ExplicitBounds,AggregationTemporality) VALUES (?,?,?,?,?,?,1)",
			attrs,
			"cluster.upstream_rq_time",
			f.start,
			at,
			sample.counts,
			[]float64{100, 200},
		); err != nil {
			t.Fatal(err)
		}
		if err := f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_metrics_sum (ResourceAttributes,MetricName,StartTimeUnix,TimeUnix,"+
				"Value,AggregationTemporality,IsMonotonic) VALUES (?,?,?,?,?,1,true)",
			attrs,
			counterMetric,
			f.start,
			at,
			sample.requests,
		); err != nil {
			t.Fatal(err)
		}
	}
	req := metricsRequest(f.start, f.start.Add(time.Minute))
	req.ResourceIds = nil
	req.Region = ""
	req.CombineResources = true
	req.ByRegion = true
	check := func(want map[string]float64) {
		t.Helper()
		got, err := f.client.QueryMetrics(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, s := range got {
			if s.GetResourceId() != "" || len(s.GetPoints()) != 1 {
				t.Fatalf("series=%v", s)
			}
			values[s.GetLabels()[regionAttribute]] = s.GetPoints()[0].GetValue()
		}
		assertPodValues(t, values, want)
	}
	req.MetricName = counterMetric
	req.Aggregation = "rate"
	check(map[string]float64{testRegion: 101.0 / 60, testWorkerRegion: 1.0 / 60})
	req.MetricName = "cluster.upstream_rq_time"
	req.Aggregation = "p95"
	check(map[string]float64{testRegion: 95.95, testWorkerRegion: 195})
}

func TestRequestMetricsDeadlineKeepsBudgetAndCallerCancellationDistinct(t *testing.T) {
	f := newQueryFixture(t)
	f.client.queryTimeout = time.Nanosecond
	req := &observabilityv1.QueryRequestMetricsRequest{
		WorkspaceId: testWorkspaceID, EnvironmentId: testEnvironmentID, ResourceId: testResourceID,
		StartTime: timestamppb.New(f.start), EndTime: timestamppb.New(f.end), IntervalSeconds: 60,
	}
	limits := RequestMetricsLimits{MaxGroups: 100, MaxMemoryBytes: 256 * 1024 * 1024, MaxRowsRead: 10000000}
	if _, err := f.client.QueryRequestMetrics(t.Context(), req, limits); !errors.Is(err, ErrRequestMetricsTooBroad) {
		t.Fatalf("local budget error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.client.QueryRequestMetrics(ctx, req, limits); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation=%v", err)
	}
}
