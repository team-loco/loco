package clickhouse

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMetricsQueryIncludesScopedSumsWithoutTemporalityConversion(t *testing.T) {
	end := time.Now()
	request := metricsRequest(end.Add(-time.Hour), end)
	request.MetricName, request.Aggregation = counterMetric, maxAggregation
	query, args := buildMetricsQuery(request, testQueryTimeout)
	clauses := []string{
		"FROM otel_metrics_sum", "UNION ALL", "AggregationTemporality", "IsMonotonic", "max(Value)",
	}
	for _, clause := range clauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q: %s", clause, query)
		}
	}
	if strings.Count(query, "WHERE WorkspaceId = ? AND EnvironmentId = ?") != 2 {
		t.Errorf("both metric tables must be scoped: %s", query)
	}
	if len(args)%2 != 0 || !slices.Equal(args[:len(args)/2], args[len(args)/2:]) {
		t.Errorf("both table scans must use identical filters: %v", args)
	}
}
