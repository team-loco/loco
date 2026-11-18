package clickhouse

import (
	"maps"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func (f *queryFixture) insertPodSample(t *testing.T, table, metric, pod string, at time.Time, value float64) {
	t.Helper()
	attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	attrs[regionAttribute], attrs[podAttribute] = testRegion, pod
	query := "INSERT INTO otel_metrics_gauge (ResourceAttributes, MetricName, TimeUnix, Value) VALUES (?, ?, ?, ?)"
	args := []any{attrs, metric, at, value}
	if table == "otel_metrics_sum" {
		query = "INSERT INTO otel_metrics_sum (ResourceAttributes, MetricName, StartTimeUnix, TimeUnix, Value, " +
			"AggregationTemporality, IsMonotonic) VALUES (?, ?, ?, ?, ?, 1, true)"
		args = []any{attrs, metric, at.Add(-time.Minute), at, value}
	}
	if err := f.conn.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// podValues queries one bucket per series and keys each value by the series' pod label.
func (f *queryFixture) podValues(t *testing.T, req *observabilityv1.QueryMetricsRequest) map[string]float64 {
	t.Helper()
	series, err := f.client.QueryMetrics(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, s := range series {
		if s.GetResourceId() != testResourceID || len(s.GetPoints()) != 1 {
			t.Fatalf("series = %v, want one point for resource %s", s, testResourceID)
		}
		pod, ok := s.GetLabels()[podAttribute]
		if !ok {
			t.Fatalf("series %v has no %s label", s, podAttribute)
		}
		values[pod] = s.GetPoints()[0].GetValue()
	}
	return values
}

func TestQueryMetricsGroupsGaugesByPod(t *testing.T) {
	f := newQueryFixture(t)
	const metric = "k8s.pod.cpu_limit_utilization"
	f.insertPodSample(t, "otel_metrics_gauge", metric, "web-a", f.start, 0.2)
	f.insertPodSample(t, "otel_metrics_gauge", metric, "web-a", f.start.Add(testSecondGaugeOffset), 0.4)
	f.insertPodSample(t, "otel_metrics_gauge", metric, "web-b", f.start, 0.9)

	req := metricsRequest(f.start, f.end)
	req.MetricName, req.GroupBy = metric, observabilityv1.MetricGroup_METRIC_GROUP_POD
	got := f.podValues(t, req)
	want := map[string]float64{"web-a": 0.3, "web-b": 0.9}
	if !maps.EqualFunc(got, want, func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }) {
		t.Fatalf("pod values = %v, want %v", got, want)
	}
}

func TestQueryMetricsGroupsRatesByPod(t *testing.T) {
	f := newQueryFixture(t)
	const metric = "k8s.pod.network.io"
	f.insertPodSample(t, "otel_metrics_sum", metric, "web-a", f.start.Add(time.Minute), 60)
	f.insertPodSample(t, "otel_metrics_sum", metric, "web-b", f.start.Add(time.Minute), 120)

	req := metricsRequest(f.start, f.start.Add(time.Minute))
	req.MetricName, req.Aggregation = metric, rateAggregation
	req.GroupBy = observabilityv1.MetricGroup_METRIC_GROUP_POD
	got := f.podValues(t, req)
	want := map[string]float64{"web-a": 1, "web-b": 2}
	if !maps.Equal(got, want) {
		t.Fatalf("pod rates = %v, want %v", got, want)
	}
}
