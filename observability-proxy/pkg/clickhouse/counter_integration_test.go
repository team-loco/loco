package clickhouse

import (
	"fmt"
	"testing"
	"time"
)

const (
	counterMetric         = "demo.payment.transactions"
	deltaTemporality      = int32(1)
	cumulativeTemporality = int32(2)
)

func TestQueryMetricsReadsCounterSamplesAndSeparatesTemporality(t *testing.T) {
	f := newQueryFixture(t)
	rows := []struct {
		workspace, environment, region, resource string
		value                                    float64
		temporality                              int32
		monotonic                                bool
	}{
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, 12, cumulativeTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, 18, cumulativeTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, 3, cumulativeTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, 2, deltaTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, 4, deltaTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testResourceID, -7, cumulativeTemporality, false},
		{testOtherWorkspaceID, testEnvironmentID, testRegion, testResourceID, 1000, cumulativeTemporality, true},
		{testWorkspaceID, testOtherEnvironmentID, testRegion, testResourceID, 2000, cumulativeTemporality, true},
		{testWorkspaceID, testEnvironmentID, testWorkerRegion, testResourceID, 3000, cumulativeTemporality, true},
		{testWorkspaceID, testEnvironmentID, testRegion, testWorkerResourceID, 4000, cumulativeTemporality, true},
	}
	batch, err := f.conn.PrepareBatch(
		t.Context(),
		"INSERT INTO otel_metrics_sum (ResourceAttributes, MetricName, TimeUnix, Value, AggregationTemporality, IsMonotonic)",
	)
	if err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		attrs := tenantAttributes(row.workspace, row.environment, row.resource)
		attrs[regionAttribute] = row.region
		timestamp := f.start.Add(time.Duration(index) * time.Second)
		if err := batch.Append(attrs, counterMetric, timestamp, row.value, row.temporality, row.monotonic); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.Exec(
		t.Context(),
		"INSERT INTO otel_metrics_gauge (ResourceAttributes, MetricName, TimeUnix, Value) VALUES (?, ?, ?, ?)",
		map[string]string{
			workspaceAttributeKey: testWorkspaceID,
			environmentAttrKey:    testEnvironmentID,
			resourceAttributeKey:  testResourceID,
			regionAttribute:       testRegion,
		},
		counterMetric,
		f.start,
		100.0,
	); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		aggregation       string
		cumulative, delta float64
	}{
		{"max", 18, 4}, {"sum", 33, 6}, {"min", 3, 2}, {"avg", 11, 3},
	} {
		t.Run(tc.aggregation, func(t *testing.T) {
			series, err := QueryMetrics(
				t.Context(),
				f.conn,
				testWorkspaceID,
				testEnvironmentID,
				testRegion,
				[]string{testResourceID},
				f.start,
				f.end,
				counterMetric,
				testIntervalSeconds,
				tc.aggregation,
				testQueryTimeout,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 4 {
				t.Fatalf("series = %v, want separate gauge, cumulative, delta and non-monotonic sums", series)
			}
			values := map[string]float64{}
			for _, s := range series {
				if s.GetResourceId() != testResourceID || len(s.GetPoints()) != 1 {
					t.Fatalf("series = %v", s)
				}
				label := s.GetLabels()
				key := fmt.Sprintf(
					"%s/%s/%s",
					label["metric_type"],
					label["aggregation_temporality"],
					label["is_monotonic"],
				)
				values[key] = s.GetPoints()[0].GetValue()
			}
			wants := map[string]float64{
				"//":          100,
				"sum/2/true":  tc.cumulative,
				"sum/1/true":  tc.delta,
				"sum/2/false": -7,
			}
			for key, want := range wants {
				if got, ok := values[key]; !ok || got != want {
					t.Errorf("series %q = %v, present %v; want %v", key, got, ok, want)
				}
			}
		})
	}
}
