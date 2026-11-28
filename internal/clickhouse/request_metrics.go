package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	codeTooManyRequestRows = 158
	codeRequestTimeout     = 159
	codeRequestMemoryLimit = 241
	codeRequestResultLimit = 396
)

var ErrRequestMetricsTooBroad = errors.New("request metrics query exceeds its limits")

type RequestMetricsLimits struct {
	MaxGroups      int32
	MaxMemoryBytes int32
	MaxRowsRead    int32
}

func requestMetricsError(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), ErrRequestMetricsTooBroad) {
		return fmt.Errorf("%w: %w", ErrRequestMetricsTooBroad, err)
	}
	exception, ok := errors.AsType[*ch.Exception](err)
	if !ok {
		return err
	}
	switch exception.Code {
	case codeTooManyRequestRows, codeRequestTimeout, codeRequestMemoryLimit, codeRequestResultLimit:
		return fmt.Errorf("%w: %w", ErrRequestMetricsTooBroad, err)
	}

	return err
}

// QueryRequestMetrics reads gateway access logs on demand. It creates no path metric series at ingest.
// limits bounds the full region/path/time aggregation, not just rows returned after aggregation.
func (c *Client) QueryRequestMetrics(
	ctx context.Context,
	req *observabilityv1.QueryRequestMetricsRequest,
	limits RequestMetricsLimits,
) (*observabilityv1.QueryRequestMetricsResponse, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, c.queryTimeout, ErrRequestMetricsTooBroad)
	defer cancel()
	query, args := requestMetricsQuery(req, limits, int(math.Ceil(c.queryTimeout.Seconds())))
	rows, err := c.query(ctx, "QueryRequestMetrics", query, args...)
	if err != nil {
		return nil, fmt.Errorf("query request metrics: %w", requestMetricsError(ctx, err))
	}
	defer rows.Close()
	response := &observabilityv1.QueryRequestMetricsResponse{}
	for rows.Next() {
		bucket := &observabilityv1.RequestMetricBucket{}
		var at int64
		var quantiles []float64
		if err := rows.Scan(
			&at,
			&bucket.Region,
			&bucket.Path,
			&bucket.Requests,
			&bucket.ServerErrors,
			&quantiles,
		); err != nil {
			return nil, fmt.Errorf("scan request metrics: %w", err)
		}
		bucket.Timestamp = timestamppb.New(time.Unix(at, 0))
		bucket.P50Millis, bucket.P95Millis, bucket.P99Millis = quantiles[0], quantiles[1], quantiles[2]
		response.Buckets = append(response.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read request metrics: %w", requestMetricsError(ctx, err))
	}
	return response, nil
}

func requestMetricsQuery(
	req *observabilityv1.QueryRequestMetricsRequest,
	limits RequestMetricsLimits,
	timeout int,
) (string, []any) {
	b := &positionalBinder{}
	filters := []string{
		"WorkspaceId = " + b.str(req.GetWorkspaceId()),
		"ResourceId = " + b.str(req.GetResourceId()),
		"EnvironmentId = " + b.str(req.GetEnvironmentId()),
		"Timestamp >= fromUnixTimestamp64Nano(" + b.num(req.GetStartTime().AsTime().UnixNano()) + ")",
		"Timestamp <= fromUnixTimestamp64Nano(" + b.num(req.GetEndTime().AsTime().UnixNano()) + ")",
		"ResourceAttributes['gateway.envoyproxy.io/owning-gateway-name'] != ''",
		"LogAttributes['upstream_cluster'] != ''",
	}
	if len(req.GetRegions()) > 0 {
		regions := make([]string, 0, len(req.GetRegions()))
		for _, region := range req.GetRegions() {
			regions = append(regions, b.str(region))
		}
		filters = append(filters, "ResourceAttributes['loco.region'] IN ("+strings.Join(regions, ", ")+")")
	}
	if req.GetPath() != "" {
		filters = append(filters, "position(splitByChar('?', LogAttributes['path'])[1], "+b.str(req.GetPath())+") > 0")
	}
	query := fmt.Sprintf(
		`SELECT toInt64(toUnixTimestamp(toStartOfInterval(Timestamp, INTERVAL %d SECOND))) AS bucket,
 ResourceAttributes['loco.region'] AS region, splitByChar('?', LogAttributes['path'])[1] AS path,
 count(), countIf(toUInt16OrZero(LogAttributes['response_code']) BETWEEN 500 AND 599),
 quantilesTDigest(0.5, 0.95, 0.99)(toFloat64OrNull(LogAttributes['duration']))
 FROM otel_logs WHERE %s AND startsWith(LogAttributes['path'], '/')
 AND isFinite(toFloat64OrNull(LogAttributes['duration'])) AND toFloat64OrNull(LogAttributes['duration']) >= 0
 GROUP BY bucket, region, path ORDER BY bucket, region, path
 SETTINGS max_execution_time = %d, max_rows_to_group_by = %d,
 max_memory_usage = %d, max_rows_to_read = %d, max_result_rows = %d,
 result_overflow_mode = 'throw', timeout_before_checking_execution_speed = 0, group_by_overflow_mode = 'throw'`,
		req.GetIntervalSeconds(),
		strings.Join(filters, " AND "),
		timeout,
		limits.MaxGroups,
		limits.MaxMemoryBytes,
		limits.MaxRowsRead,
		limits.MaxGroups,
	)
	return query, b.args
}
