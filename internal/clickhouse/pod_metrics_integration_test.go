package clickhouse

import (
	"math"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	podA = "web-a"
	podB = "web-b"
)

type podSample struct {
	workspaceID, resourceID, pod string
	at                           time.Time
	value                        float64
}

// insertPodSamples writes gauge samples, or delta sums covering the minute before each sample.
func (f *queryFixture) insertPodSamples(t *testing.T, metric string, sum bool, samples ...podSample) {
	t.Helper()
	for _, sample := range samples {
		attrs := tenantAttributes(sample.workspaceID, testEnvironmentID, sample.resourceID)
		attrs[regionAttribute], attrs[podAttribute] = testRegion, sample.pod
		query := "INSERT INTO otel_metrics_gauge (ResourceAttributes, MetricName, TimeUnix, Value) VALUES (?, ?, ?, ?)"
		args := []any{attrs, metric, sample.at, sample.value}
		if sum {
			query = "INSERT INTO otel_metrics_sum (ResourceAttributes, MetricName, StartTimeUnix, TimeUnix, Value, " +
				"AggregationTemporality, IsMonotonic) VALUES (?, ?, ?, ?, ?, 1, true)"
			args = []any{attrs, metric, sample.at.Add(-time.Minute), sample.at, sample.value}
		}
		if err := f.conn.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
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

func assertPodValues(t *testing.T, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("pod values = %v, want %v", got, want)
	}
	for pod, value := range want {
		if math.Abs(got[pod]-value) > 1e-9 {
			t.Fatalf("pod values = %v, want %v", got, want)
		}
	}
}

// otherScopes are samples for the same pod names under another resource and another workspace.
func otherScopes(at time.Time) []podSample {
	return []podSample{
		{testWorkspaceID, testWorkerResourceID, podA, at, 100},
		{testWorkspaceID, testWorkerResourceID, "worker-a", at, 100},
		{testOtherWorkspaceID, testResourceID, podA, at, 1000},
		{testOtherWorkspaceID, testResourceID, "web-other", at, 1000},
	}
}

func TestQueryMetricsGroupsGaugesByPod(t *testing.T) {
	f := newQueryFixture(t)
	const metric = "k8s.pod.cpu_limit_utilization"
	f.insertPodSamples(t, metric, false,
		podSample{testWorkspaceID, testResourceID, podA, f.start, 0.2},
		podSample{testWorkspaceID, testResourceID, podA, f.start.Add(testSecondGaugeOffset), 0.4},
		podSample{testWorkspaceID, testResourceID, podB, f.start, 0.9},
	)
	f.insertPodSamples(t, metric, false, otherScopes(f.start)...)

	req := metricsRequest(f.start, f.end)
	req.MetricName, req.ByPod = metric, true
	assertPodValues(t, f.podValues(t, req), map[string]float64{podA: 0.3, podB: 0.9})
}

func TestQueryMetricsGroupsRatesByPod(t *testing.T) {
	f := newQueryFixture(t)
	const metric = "k8s.pod.network.io"
	at := f.start.Add(time.Minute)
	f.insertPodSamples(t, metric, true,
		podSample{testWorkspaceID, testResourceID, podA, at, 60},
		podSample{testWorkspaceID, testResourceID, podB, at, 120},
	)
	f.insertPodSamples(t, metric, true, otherScopes(at)...)

	req := metricsRequest(f.start, at)
	req.MetricName, req.Aggregation, req.ByPod = metric, rateAggregation, true
	assertPodValues(t, f.podValues(t, req), map[string]float64{podA: 1, podB: 2})
}
