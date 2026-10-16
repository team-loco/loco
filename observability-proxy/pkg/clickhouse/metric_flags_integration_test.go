package clickhouse

import (
	"fmt"
	"testing"
	"time"
)

func TestQueryMetricsExcludesUnrecordedPointsWithoutDroppingZeroValues(t *testing.T) {
	f := newQueryFixture(t)
	for _, table := range []string{"otel_metrics_gauge", "otel_metrics_sum"} {
		t.Run(table, func(t *testing.T) {
			metric := "dogfood.staleness." + table
			attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
			attrs[regionAttribute] = testRegion
			for index, row := range []struct {
				value float64
				flags uint32
			}{
				{value: 0, flags: 0},
				{value: 900, flags: 1},
				{value: 40, flags: 2},
				{value: 1000, flags: 3},
			} {
				query := fmt.Sprintf(
					"INSERT INTO %s (ResourceAttributes, MetricName, TimeUnix, Value, Flags) VALUES (?, ?, ?, ?, ?)",
					table,
				)
				if err := f.conn.Exec(
					t.Context(),
					query,
					attrs,
					metric,
					f.start.Add(time.Duration(index)*time.Second),
					row.value,
					row.flags,
				); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{metric, metric + ".unrecorded"} {
				query := fmt.Sprintf(
					"INSERT INTO %s (ResourceAttributes, MetricName, TimeUnix, Value, Flags) VALUES (?, ?, ?, ?, ?)",
					table,
				)
				if err := f.conn.Exec(
					t.Context(),
					query,
					attrs,
					name,
					f.start.Add(time.Minute),
					0.0,
					uint32(1),
				); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				aggregation string
				want        float64
			}{
				{aggregation: "avg", want: 20},
				{aggregation: "sum", want: 40},
				{aggregation: "min", want: 0},
				{aggregation: "max", want: 40},
			} {
				t.Run(tc.aggregation, func(t *testing.T) {
					series, err := QueryMetrics(
						t.Context(),
						f.conn,
						testWorkspaceID,
						testEnvironmentID,
						testRegion,
						[]string{
							testResourceID,
						},
						f.start,
						f.end,
						metric,
						testIntervalSeconds,
						tc.aggregation,
						testQueryTimeout,
					)
					if err != nil {
						t.Fatal(err)
					}
					if len(series) != 1 || len(series[0].GetPoints()) != 1 ||
						series[0].GetPoints()[0].GetValue() != tc.want {
						t.Fatalf("series = %v, want one recorded bucket with value %v", series, tc.want)
					}
				})
			}
			series, err := QueryMetrics(
				t.Context(),
				f.conn,
				testWorkspaceID,
				testEnvironmentID,
				testRegion,
				[]string{
					testResourceID,
				},
				f.start,
				f.end,
				metric+".unrecorded",
				testIntervalSeconds,
				"avg",
				testQueryTimeout,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(series) != 0 {
				t.Fatalf("unrecorded-only series = %v, want none", series)
			}
		})
	}
}
