package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Client) QueryLogBuckets(
	ctx context.Context,
	q LogQuery,
	intervalSeconds int32,
	group observabilityv1.LogBucketGroup,
) (*observabilityv1.QueryLogBucketsResponse, error) {
	b := &positionalBinder{}
	where := logFilters(q, b)
	where = append(where, "Timestamp <= fromUnixTimestamp64Nano("+b.num(q.End.UnixNano())+")")
	column := "SeverityText"
	if group == observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_RESOURCE {
		column = resourceColumn
	}
	query := fmt.Sprintf("SELECT toStartOfInterval(Timestamp, INTERVAL %d SECOND) AS bucket, %s, count() "+
		"FROM otel_logs WHERE %s GROUP BY bucket, %s ORDER BY bucket, %s SETTINGS max_execution_time = %d",
		intervalSeconds, column, strings.Join(where, " AND "), column, column, int(c.queryTimeout.Seconds()))
	rows, err := c.conn.Query(ctx, query, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query log buckets: %w", err)
	}
	defer rows.Close()
	response := &observabilityv1.QueryLogBucketsResponse{}
	for rows.Next() {
		var start time.Time
		var value string
		var count uint64
		if err := rows.Scan(&start, &value, &count); err != nil {
			return nil, fmt.Errorf("scan log bucket: %w", err)
		}
		bucket := &observabilityv1.LogBucket{StartTime: timestamppb.New(start), Count: count}
		if group == observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_RESOURCE {
			bucket.ResourceId = value
		} else {
			bucket.Severity = value
		}
		response.Buckets = append(response.Buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("log bucket rows: %w", err)
	}
	return response, nil
}
