package clickhouse

import (
	"slices"
	"testing"
	"time"
)

const (
	precisionMetricName = "loco.test.metric_precision"
	precisionSumTable   = "otel_metrics_sum"
)

func TestQueryMetricsRespectsFractionalTimeBounds(t *testing.T) {
	fixture := newQueryFixture(t)
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	for _, table := range []string{"otel_metrics_gauge", precisionSumTable} {
		columns := gaugeInsertColumns
		if table == precisionSumTable {
			columns += ", AggregationTemporality, IsMonotonic"
		}
		batch, err := fixture.conn.PrepareBatch(t.Context(), "INSERT INTO "+table+" ("+columns+")")
		if err != nil {
			t.Fatal(err)
		}
		for _, point := range []struct {
			offset time.Duration
			value  float64
		}{{100 * time.Millisecond, 2}, {900 * time.Millisecond, 8}} {
			attributes := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
			timestamp := base.Add(point.offset)
			values := []any{attributes, precisionMetricName, map[string]string{}, timestamp, point.value}
			if table == precisionSumTable {
				values = append(values, int32(2), true)
			}
			if err := batch.Append(values...); err != nil {
				t.Fatal(err)
			}
		}
		if err := batch.Send(); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name       string
		start, end time.Duration
		want       []float64
	}{
		{"whole second", 0, time.Second, []float64{5}},
		{"first half", 50 * time.Millisecond, 500 * time.Millisecond, []float64{2}},
		{"second half", 500 * time.Millisecond, 950 * time.Millisecond, []float64{8}},
		{"gap", 300 * time.Millisecond, 700 * time.Millisecond, nil},
		{"exact point", 900 * time.Millisecond, 900 * time.Millisecond, []float64{8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			series, err := QueryMetrics(t.Context(), fixture.conn, testWorkspaceID, testEnvironmentID, "",
				[]string{testResourceID}, base.Add(tc.start), base.Add(tc.end), precisionMetricName,
				1, avgAggregation, testQueryTimeout)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(series) != 0 {
					t.Fatalf("gap returned %v", series)
				}
				return
			}
			if len(series) != 2 {
				t.Fatalf("series count = %d, want gauge and sum", len(series))
			}
			for _, metric := range series {
				var values []float64
				for _, point := range metric.GetPoints() {
					values = append(values, point.GetValue())
					if !point.GetTimestamp().AsTime().Equal(base) {
						t.Errorf("bucket = %v, want %v", point.GetTimestamp(), base)
					}
				}
				if !slices.Equal(values, tc.want) {
					t.Errorf("%v = %v, want %v", metric.GetLabels(), values, tc.want)
				}
			}
		})
	}
}
