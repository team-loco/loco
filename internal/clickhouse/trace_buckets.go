package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

// QueryTraceBuckets groups the traces ListTraces would list by the bucket their start falls in, and
// summarizes them all in the response's total.
func (c *Client) QueryTraceBuckets(
	ctx context.Context,
	req *observabilityv1.QueryTraceBucketsRequest,
) (*observabilityv1.QueryTraceBucketsResponse, error) {
	summaries, args := buildTraceSummaryQuery(req)
	query := fmt.Sprintf("SELECT toStartOfInterval(start_time, INTERVAL %d SECOND) AS bucket, count(), "+
		"countIf(status = ?), quantiles(0.5, 0.95)(duration_nanos) FROM (%s) GROUP BY bucket WITH TOTALS "+
		"SETTINGS max_execution_time = %d",
		req.GetIntervalSeconds(), summaries, int(c.queryTimeout.Seconds()))
	args = append([]any{int32(observabilityv1.TraceStatus_TRACE_STATUS_ERROR)}, args...)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query trace buckets: %w", err)
	}
	defer rows.Close()
	filled := make(map[int64]*observabilityv1.TraceBucket)
	var start time.Time
	var traces, failed uint64
	var quantiles []float64
	for rows.Next() {
		if err = rows.Scan(&start, &traces, &failed, &quantiles); err != nil {
			return nil, fmt.Errorf("scan trace bucket: %w", err)
		}
		filled[start.UnixNano()] = traceBucket(traces, failed, quantiles)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("trace bucket rows: %w", err)
	}
	total := &observabilityv1.TraceBucket{}
	err = rows.Totals(&start, &traces, &failed, &quantiles)
	if err == nil {
		total = traceBucket(traces, failed, quantiles)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("scan trace bucket totals: %w", err)
	}
	interval := int64(req.GetIntervalSeconds()) * int64(time.Second)
	first := bucketStart(req.GetStartTime().AsTime(), req.GetIntervalSeconds())
	count := BucketCount(req.GetStartTime().AsTime(), req.GetEndTime().AsTime(), req.GetIntervalSeconds())
	buckets := make([]*observabilityv1.TraceBucket, 0, count)
	for i := range count {
		at := first + i*interval
		bucket, ok := filled[at]
		if !ok {
			bucket = &observabilityv1.TraceBucket{}
		}
		bucket.StartTime = timestamppb.New(time.Unix(0, at))
		buckets = append(buckets, bucket)
	}
	return &observabilityv1.QueryTraceBucketsResponse{Buckets: buckets, Total: total}, nil
}

func traceBucket(traces, failed uint64, quantiles []float64) *observabilityv1.TraceBucket {
	bucket := &observabilityv1.TraceBucket{TraceCount: traces, ErrorCount: failed}
	// An empty group's quantiles are NaN.
	if traces > 0 {
		bucket.P50DurationNanos = uint64(math.Round(quantiles[0]))
		bucket.P95DurationNanos = uint64(math.Round(quantiles[1]))
	}
	return bucket
}

// bucketStart returns the Unix nanosecond start of the interval-second bucket holding t, aligned
// to the Unix epoch like ClickHouse's toStartOfInterval.
func bucketStart(t time.Time, intervalSeconds int32) int64 {
	interval := int64(intervalSeconds) * int64(time.Second)
	return floorDiv(t.UnixNano(), interval) * interval
}

// BucketCount returns the number of interval-second buckets from the one holding start to the one
// holding end.
func BucketCount(start, end time.Time, intervalSeconds int32) int64 {
	interval := int64(intervalSeconds) * int64(time.Second)
	return floorDiv(end.UnixNano(), interval) - floorDiv(start.UnixNano(), interval) + 1
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}
