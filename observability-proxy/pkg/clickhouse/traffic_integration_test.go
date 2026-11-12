package clickhouse

import (
	"math"
	"testing"
	"time"
)

func TestGatewayRatesAndLatency(t *testing.T) {
	f := newQueryFixture(t)
	start := f.start.Truncate(time.Minute)
	for _, sample := range []struct {
		at               int
		value            float64
		temporality      int32
		pod, environment string
	}{
		{-10, 100, 2, "a", testEnvironmentID},
		{10, 120, 2, "a", testEnvironmentID},
		{30, 5, 2, "a", testEnvironmentID},
		{50, 25, 2, "a", testEnvironmentID},
		{10, 10, 1, "b", testEnvironmentID},
		{30, 20, 1, "b", testEnvironmentID},
		{50, 20, 1, "b", testEnvironmentID},
		{10, 9999, 1, "b", testOtherEnvironmentID},
	} {
		resourceAttrs := tenantAttributes(testWorkspaceID, sample.environment, testResourceID)
		resourceAttrs[regionAttribute], resourceAttrs["k8s.pod.uid"] = testRegion, sample.pod
		pointStart := start.Add(-time.Hour)
		if sample.temporality == 1 {
			pointStart = start.Add(time.Duration(sample.at-20) * time.Second)
		}
		err := f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_metrics_sum (ResourceAttributes, MetricName, StartTimeUnix, TimeUnix, Value, "+
				"AggregationTemporality, IsMonotonic) VALUES (?, ?, ?, ?, ?, ?, true)",
			resourceAttrs,
			counterMetric,
			pointStart,
			start.Add(time.Duration(sample.at)*time.Second),
			sample.value,
			sample.temporality,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := metricsRequest(start, start.Add(time.Minute))
	req.MetricName, req.Aggregation, req.IntervalSeconds = counterMetric, "rate", 60
	series, err := f.client.QueryMetrics(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	// The first scrape interval overlaps the window by half; the reset contributes five.
	want := (10.0 + 5 + 20 + 5 + 20 + 20) / 60
	if len(series) != 1 || len(series[0].GetPoints()) != 1 ||
		math.Abs(series[0].GetPoints()[0].GetValue()-want) > 1e-9 {
		t.Fatalf("rates = %v, want %v", series, want)
	}
	for _, sample := range []struct {
		env         string
		counts      []uint64
		temporality int32
		pod         string
		at          int
	}{
		{testEnvironmentID, []uint64{80, 10, 10}, 2, "a", -10},
		{testEnvironmentID, []uint64{90, 20, 10}, 2, "a", 10},
		{testEnvironmentID, []uint64{10, 10, 0}, 1, "b", 10},
		{testOtherEnvironmentID, []uint64{0, 0, 9999}, 1, "c", 10},
	} {
		resourceAttrs := tenantAttributes(testWorkspaceID, sample.env, testResourceID)
		resourceAttrs[regionAttribute], resourceAttrs["k8s.pod.uid"] = testRegion, sample.pod
		pointStart := start.Add(-time.Hour)
		if sample.temporality == 1 {
			pointStart = start.Add(-10 * time.Second)
		}
		err = f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_metrics_histogram (ResourceAttributes, MetricName, StartTimeUnix, TimeUnix, "+
				"BucketCounts, ExplicitBounds, AggregationTemporality) VALUES (?, ?, ?, ?, ?, ?, ?)",
			resourceAttrs,
			"cluster.upstream_rq_time",
			pointStart,
			start.Add(time.Duration(sample.at)*time.Second),
			sample.counts,
			[]float64{100, 200},
			sample.temporality,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	req.MetricName, req.Aggregation = "cluster.upstream_rq_time", trafficQuantile
	series, err = f.client.QueryMetrics(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || len(series[0].GetPoints()) != 1 || math.Abs(series[0].GetPoints()[0].GetValue()-190) > 1e-9 {
		t.Fatalf("p95 = %v, want 190", series)
	}
}

func TestGatewayRatesFilterResponseClass(t *testing.T) {
	f := newQueryFixture(t)
	for _, class := range []string{"2", "5"} {
		attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
		attrs[regionAttribute] = testRegion
		err := f.conn.Exec(
			t.Context(),
			"INSERT INTO otel_metrics_sum (ResourceAttributes, Attributes, MetricName, StartTimeUnix, TimeUnix, "+
				"Value, AggregationTemporality, IsMonotonic) VALUES (?, ?, ?, ?, ?, ?, 1, true)",
			attrs,
			map[string]string{"envoy.response_code_class": class},
			"cluster.upstream_rq_xx",
			f.start,
			f.start.Add(time.Minute),
			60.0,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := metricsRequest(f.start, f.start.Add(time.Minute))
	req.MetricName, req.Aggregation = "cluster.upstream_rq_xx", "rate"
	req.Attributes = map[string]string{"envoy.response_code_class": "5"}
	series, err := f.client.QueryMetrics(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || len(series[0].GetPoints()) != 1 || series[0].GetPoints()[0].GetValue() != 1 {
		t.Fatalf("5xx rate = %v, want 1/s", series)
	}
}
