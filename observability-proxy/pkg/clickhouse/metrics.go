package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// QueryMetrics executes a metric aggregation query against ClickHouse.
func QueryMetrics(
	ctx context.Context,
	conn driver.Conn,
	workspaceID string,
	resourceIDs []string,
	startTime, endTime time.Time,
	metricName string,
	intervalSeconds int32,
	aggregation string,
	queryTimeout int,
) ([]*observabilityv1.MetricSeries, error) {
	aggFunc := mapAggregation(aggregation)
	interval := intervalSeconds
	if interval <= 0 {
		interval = 60
	}

	var whereParts []string
	var args []any

	// Mandatory filters
	whereParts = append(whereParts, fmt.Sprintf("%s = ?", workspaceAttr))
	args = append(args, workspaceID)

	if len(resourceIDs) > 0 {
		placeholders := make([]string, len(resourceIDs))
		for i, rid := range resourceIDs {
			placeholders[i] = "?"
			args = append(args, rid)
		}
		whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", resourceAttr, strings.Join(placeholders, ",")))
	}

	whereParts = append(whereParts, "MetricName = ?")
	args = append(args, metricName)

	whereParts = append(whereParts, "TimeUnix >= ?", "TimeUnix <= ?")
	args = append(args, startTime, endTime)

	query := fmt.Sprintf(
		`SELECT
			%s AS resource_id,
			toStartOfInterval(TimeUnix, INTERVAL %d SECOND) AS bucket,
			%s(Value) AS agg_value
		FROM otel_metrics_gauge
		WHERE %s
		GROUP BY resource_id, bucket
		ORDER BY resource_id, bucket
		SETTINGS max_execution_time = %d`,
		resourceAttr,
		interval,
		aggFunc,
		strings.Join(whereParts, " AND "),
		queryTimeout,
	)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse metrics query: %w", err)
	}
	defer rows.Close()

	// Group results by resource_id
	seriesMap := make(map[string]*observabilityv1.MetricSeries)

	for rows.Next() {
		var (
			resourceID string
			bucket     time.Time
			value      float64
		)
		if err := rows.Scan(&resourceID, &bucket, &value); err != nil {
			return nil, fmt.Errorf("scan metric row: %w", err)
		}

		series, ok := seriesMap[resourceID]
		if !ok {
			series = &observabilityv1.MetricSeries{
				ResourceId: resourceID,
			}
			seriesMap[resourceID] = series
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

func mapAggregation(agg string) string {
	switch agg {
	case "sum":
		return "sum"
	case "min":
		return "min"
	case "max":
		return "max"
	case "p50":
		return "quantile(0.5)"
	case "p95":
		return "quantile(0.95)"
	case "p99":
		return "quantile(0.99)"
	default:
		return "avg"
	}
}
