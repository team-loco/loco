package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// traceRequest holds the scope and range ListTracesRequest and QueryTraceBucketsRequest share.
type traceRequest interface {
	GetScope() *observabilityv1.TraceScope
	GetStartTime() *timestamppb.Timestamp
	GetEndTime() *timestamppb.Timestamp
}

// buildTraceSummaryQuery summarizes the traces of the request's spans that match filter, binding
// every value through b.
func buildTraceSummaryQuery(req traceRequest, filter TraceFilter, b *positionalBinder) string {
	spans := func() string {
		return strings.Join(append(traceScope(req.GetScope(), b),
			"Timestamp >= fromUnixTimestamp64Nano("+b.num(req.GetStartTime().AsTime().UnixNano())+")",
			"Timestamp <= fromUnixTimestamp64Nano("+b.num(req.GetEndTime().AsTime().UnixNano())+")",
		), " AND ")
	}
	query := "SELECT TraceId, argMin(tuple(SpanName, ServiceName, ResourceId), " +
		"tuple(ParentSpanId != '', Timestamp, SpanId)) AS root, min(Timestamp) AS start_time, " +
		"toUInt64(max(toInt128(toUnixTimestamp64Nano(Timestamp)) + Duration) - " +
		"toInt128(toUnixTimestamp64Nano(start_time))) AS duration_nanos, " +
		"if(countIf(StatusCode = " + b.str(ptrace.StatusCodeError.String()) + ") > 0, toInt32(" +
		b.num(int64(observabilityv1.TraceStatus_TRACE_STATUS_ERROR)) + "), toInt32(" +
		b.num(int64(observabilityv1.TraceStatus_TRACE_STATUS_OK)) + ")) AS status, " +
		"uniqExact(SpanId) AS span_count FROM otel_traces WHERE " + spans() + " GROUP BY TraceId"
	if filter.Query != nil {
		query += " HAVING " + traceFilterSQL(filter.Query, filter.Resources, spans, b)
	}
	return query
}

func (c *Client) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
	filter TraceFilter,
	limit int32,
) (*observabilityv1.ListTracesResponse, error) {
	b := &positionalBinder{}
	query := buildTraceSummaryQuery(req, filter, b)
	args := b.args
	settings := fmt.Sprintf(" SETTINGS max_execution_time = %d", int(c.queryTimeout.Seconds()))
	var total uint64
	if err := c.conn.QueryRow(ctx, "SELECT count() FROM ("+query+")"+settings, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count trace summaries: %w", err)
	}
	sort := "start_time"
	if req.GetSort() == observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION {
		sort = "duration_nanos"
	}
	direction := " DESC"
	if req.GetAscending() {
		direction = " ASC"
	}
	query = "SELECT TraceId, root.1, root.2, root.3, start_time, duration_nanos, status, span_count FROM (" +
		query + ") ORDER BY " + sort + direction + ", TraceId ASC LIMIT ? OFFSET ?" + settings
	args = append(args, limit, req.GetOffset())
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query trace summaries: %w", err)
	}
	defer rows.Close()
	summaries := make([]*observabilityv1.TraceSummary, 0)
	for rows.Next() {
		var id, name, service, resource string
		var status int32
		var start time.Time
		var duration, count uint64
		if err := rows.Scan(&id, &name, &service, &resource, &start, &duration, &status, &count); err != nil {
			return nil, fmt.Errorf("scan trace summary: %w", err)
		}
		summaries = append(summaries, &observabilityv1.TraceSummary{
			TraceId:       id,
			RootSpanName:  name,
			ServiceName:   service,
			ResourceId:    resource,
			StartTime:     timestamppb.New(start),
			DurationNanos: duration,
			Status:        observabilityv1.TraceStatus(status),
			SpanCount:     count,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trace summary rows: %w", err)
	}
	return &observabilityv1.ListTracesResponse{Summaries: summaries, TotalCount: total}, nil
}
