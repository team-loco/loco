package clickhouse

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	regionAttribute     = "loco.region"
	sumMetricType       = "sum"
	noRecordedValueFlag = 1
)

// aggregationFunctions maps the aggregations the QueryMetricsRequest proto rule allows to their
// ClickHouse functions; update both together.
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
	if req.GetAggregation() == "rate" {
		return c.queryTrafficMetrics(ctx, req, false)
	}
	query, args := buildMetricsQuery(req, c.queryTimeout)

	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse metrics query: %w", err)
	}
	defer rows.Close()

	type seriesKey struct {
		resourceID  string
		pod         string
		metricType  string
		temporality int32
		monotonic   bool
	}
	seriesMap := make(map[seriesKey]*observabilityv1.MetricSeries)

	for rows.Next() {
		var (
			resourceID  string
			pod         string
			bucket      time.Time
			value       float64
			metricType  string
			temporality int32
			monotonic   bool
		)
		if err := rows.Scan(&resourceID, &pod, &bucket, &value, &metricType, &temporality, &monotonic); err != nil {
			return nil, fmt.Errorf("scan metric row: %w", err)
		}

		key := seriesKey{
			resourceID:  resourceID,
			pod:         pod,
			metricType:  metricType,
			temporality: temporality,
			monotonic:   monotonic,
		}
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
			labelPod(series, req, pod)
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
	if strings.HasPrefix(req.GetAggregation(), "p") {
		histograms, err := c.queryTrafficMetrics(ctx, req, true)
		if err != nil {
			return nil, err
		}
		result = append(result, histograms...)
	}
	return result, nil
}

func buildMetricsQuery(req *observabilityv1.QueryMetricsRequest, queryTimeout time.Duration) (string, []any) {
	aggFunc := aggregationFunctions[req.GetAggregation()]
	interval := req.GetIntervalSeconds()

	b := &positionalBinder{}
	whereParts := metricsFilters(req, b)
	whereParts = append(whereParts,
		"MetricName = "+b.str(req.GetMetricName()),
		"TimeUnix >= fromUnixTimestamp64Nano("+b.num(req.GetStartTime().AsTime().UnixNano())+")",
		"TimeUnix <= fromUnixTimestamp64Nano("+b.num(req.GetEndTime().AsTime().UnixNano())+")",
		fmt.Sprintf("bitAnd(Flags, %d) = 0", noRecordedValueFlag))

	where := strings.Join(whereParts, " AND ")
	series := resourceColumn + ", " + podColumn(req) + " AS Pod"
	source := fmt.Sprintf(
		"SELECT %s, TimeUnix, Value, 'gauge' AS MetricType, toInt32(0) AS AggregationTemporality, false AS IsMonotonic "+
			"FROM otel_metrics_gauge WHERE %s UNION ALL "+
			"SELECT %s, TimeUnix, Value, 'sum' AS MetricType, AggregationTemporality, IsMonotonic "+
			"FROM otel_metrics_sum WHERE %s",
		series,
		where,
		series,
		where,
	)
	query := fmt.Sprintf(
		"SELECT %s, Pod, toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS bucket, %s(Value) AS agg_value, "+
			"MetricType, AggregationTemporality, IsMonotonic FROM (%s) "+
			"GROUP BY %s, Pod, bucket, MetricType, AggregationTemporality, IsMonotonic "+
			"ORDER BY %s, Pod, bucket SETTINGS max_execution_time = %d",
		resourceColumn, interval, aggFunc, source, resourceColumn, resourceColumn, int(queryTimeout.Seconds()),
	)
	// The WHERE clause appears once per table of the UNION.
	return query, append(b.args, b.args...)
}

// metricsFilters is the scope every metrics query shares, binding every value through b.
func metricsFilters(req *observabilityv1.QueryMetricsRequest, b binder) []string {
	whereParts := []string{workspaceColumn + " = " + b.str(req.GetWorkspaceId())}
	if req.GetEnvironmentId() != "" {
		whereParts = append(whereParts, environmentColumn+" = "+b.str(req.GetEnvironmentId()))
	}
	if req.GetRegion() != "" {
		whereParts = append(whereParts, "ResourceAttributes["+b.str(regionAttribute)+"] = "+b.str(req.GetRegion()))
	}
	if len(req.GetResourceIds()) > 0 {
		whereParts = append(whereParts, logIn(resourceColumn, req.GetResourceIds(), b))
	}
	for _, key := range slices.Sorted(maps.Keys(req.GetAttributes())) {
		whereParts = append(whereParts, "Attributes["+b.str(key)+"] = "+b.str(req.GetAttributes()[key]))
	}
	return whereParts
}

// podColumn is the pod a series belongs to, or one empty pod per resource without pod grouping.
func podColumn(req *observabilityv1.QueryMetricsRequest) string {
	if req.GetByPod() {
		return "ResourceAttributes['" + podAttribute + "']"
	}
	return "''"
}

func labelPod(series *observabilityv1.MetricSeries, req *observabilityv1.QueryMetricsRequest, pod string) {
	if !req.GetByPod() {
		return
	}
	if series.Labels == nil {
		series.Labels = map[string]string{}
	}
	series.Labels[podAttribute] = pod
}
