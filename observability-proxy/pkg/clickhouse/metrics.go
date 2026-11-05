package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultIntervalSeconds = 60
	regionAttribute        = "loco.region"
	sumMetricType          = "sum"
	noRecordedValueFlag    = 1
)

var aggregationFunctions = map[string]string{
	"avg": "avg",
	"sum": "sum",
	"min": "min",
	"max": "max",
	"p50": "quantile(0.5)",
	"p95": "quantile(0.95)",
	"p99": "quantile(0.99)",
}

func (c *Client) QueryMetrics(
	ctx context.Context,
	req *observabilityv1.QueryMetricsRequest,
) ([]*observabilityv1.MetricSeries, error) {
	query, args := buildMetricsQuery(req, c.queryTimeout)

	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse metrics query: %w", err)
	}
	defer rows.Close()

	type seriesKey struct {
		resourceID  string
		metricType  string
		temporality int32
		monotonic   bool
	}
	seriesMap := make(map[seriesKey]*observabilityv1.MetricSeries)

	for rows.Next() {
		var (
			resourceID  string
			bucket      time.Time
			value       float64
			metricType  string
			temporality int32
			monotonic   bool
		)
		if err := rows.Scan(&resourceID, &bucket, &value, &metricType, &temporality, &monotonic); err != nil {
			return nil, fmt.Errorf("scan metric row: %w", err)
		}

		key := seriesKey{resourceID: resourceID, metricType: metricType, temporality: temporality, monotonic: monotonic}
		series, ok := seriesMap[key]
		if !ok {
			series = &observabilityv1.MetricSeries{
				ResourceId: resourceID,
			}
			if metricType == sumMetricType {
				series.Labels = map[string]string{
					"metric_type":             metricType,
					"aggregation_temporality": strconv.FormatInt(int64(temporality), 10),
					"is_monotonic":            strconv.FormatBool(monotonic),
				}
			}
			seriesMap[key] = series
		}
		series.Points = append(series.Points, &observabilityv1.MetricPoint{
			Timestamp: timestamppb.New(bucket),
			Value:     value,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	result := make([]*observabilityv1.MetricSeries, 0, len(seriesMap))
	for _, s := range seriesMap {
		result = append(result, s)
	}
	return result, nil
}

func buildMetricsQuery(req *observabilityv1.QueryMetricsRequest, queryTimeout time.Duration) (string, []any) {
	aggFunc := aggregationFunctions[req.GetAggregation()]
	interval := req.GetIntervalSeconds()
	if interval <= 0 {
		interval = defaultIntervalSeconds
	}

	whereParts := []string{workspaceColumn + " = ?"}
	args := []any{req.GetWorkspaceId()}

	if req.GetEnvironmentId() != "" {
		whereParts = append(whereParts, environmentColumn+" = ?")
		args = append(args, req.GetEnvironmentId())
	}

	if req.GetRegion() != "" {
		whereParts = append(whereParts, "ResourceAttributes[?] = ?")
		args = append(args, regionAttribute, req.GetRegion())
	}

	if len(req.GetResourceIds()) > 0 {
		clause, values := inFilter(resourceColumn, req.GetResourceIds())
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	whereParts = append(whereParts,
		"MetricName = ?",
		"TimeUnix >= fromUnixTimestamp64Nano(?)",
		"TimeUnix <= fromUnixTimestamp64Nano(?)",
		fmt.Sprintf("bitAnd(Flags, %d) = 0", noRecordedValueFlag))
	start := req.GetStartTime().AsTime().UnixNano()
	end := req.GetEndTime().AsTime().UnixNano()
	args = append(args, req.GetMetricName(), start, end)

	where := strings.Join(whereParts, " AND ")
	source := fmt.Sprintf(
		"SELECT %s, TimeUnix, Value, 'gauge' AS MetricType, toInt32(0) AS AggregationTemporality, false AS IsMonotonic "+
			"FROM otel_metrics_gauge WHERE %s UNION ALL "+
			"SELECT %s, TimeUnix, Value, 'sum' AS MetricType, AggregationTemporality, IsMonotonic "+
			"FROM otel_metrics_sum WHERE %s",
		resourceColumn,
		where,
		resourceColumn,
		where,
	)
	query := fmt.Sprintf(
		"SELECT %s, toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS bucket, %s(Value) AS agg_value, "+
			"MetricType, AggregationTemporality, IsMonotonic FROM (%s) "+
			"GROUP BY %s, bucket, MetricType, AggregationTemporality, IsMonotonic "+
			"ORDER BY %s, bucket SETTINGS max_execution_time = %d",
		resourceColumn, interval, aggFunc, source, resourceColumn, resourceColumn, int(queryTimeout.Seconds()),
	)
	args = append(args, args...)

	return query, args
}

// IsAggregation reports whether name is an aggregation QueryMetrics accepts.
func IsAggregation(name string) bool {
	_, ok := aggregationFunctions[name]
	return ok
}
