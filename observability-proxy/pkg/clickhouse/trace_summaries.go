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

func buildTraceSummaryQuery(req *observabilityv1.QueryTracesRequest) (string, []any) {
	where, args := traceScope(req)
	filters := req.GetSummary()
	having := []string{"1"}
	if filters.MinDurationNanos != nil {
		having = append(having, "duration_nanos >= ?")
		args = append(args, filters.GetMinDurationNanos())
	}
	if filters.MaxDurationNanos != nil {
		having = append(having, "duration_nanos <= ?")
		args = append(args, filters.GetMaxDurationNanos())
	}
	if filters.GetStatus() != observabilityv1.TraceStatus_TRACE_STATUS_UNSPECIFIED {
		having = append(having, "status = ?")
		args = append(args, int32(filters.GetStatus()))
	}
	for _, term := range filters.GetRootNameTerms() {
		having = append(having, "positionCaseInsensitiveUTF8(root.1, ?) > 0")
		args = append(args, term)
	}
	args = append([]any{
		ptrace.StatusCodeError.String(),
		int32(observabilityv1.TraceStatus_TRACE_STATUS_ERROR),
		int32(observabilityv1.TraceStatus_TRACE_STATUS_OK),
	}, args...)
	return "SELECT TraceId, argMin(tuple(SpanName, ServiceName, ResourceId), " +
		"tuple(ParentSpanId != '', Timestamp, SpanId)) AS root, min(Timestamp) AS start_time, " +
		"toUInt64(max(toInt128(toUnixTimestamp64Nano(Timestamp)) + Duration) - " +
		"toInt128(toUnixTimestamp64Nano(start_time))) AS duration_nanos, " +
		"if(countIf(StatusCode = ?) > 0, toInt32(?), toInt32(?)) AS status, " +
		"uniqExact(SpanId) AS span_count FROM otel_traces WHERE " + strings.Join(where, " AND ") +
		" GROUP BY TraceId HAVING " + strings.Join(having, " AND "), args
}

func (c *Client) queryTraceSummaries(
	ctx context.Context,
	req *observabilityv1.QueryTracesRequest,
	limit int32,
) (*observabilityv1.QueryTracesResponse, error) {
	query, args := buildTraceSummaryQuery(req)
	settings := fmt.Sprintf(" SETTINGS max_execution_time = %d", int(c.queryTimeout.Seconds()))
	var total uint64
	if err := c.conn.QueryRow(ctx, "SELECT count() FROM ("+query+")"+settings, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count trace summaries: %w", err)
	}
	sort := "start_time"
	if req.GetSummary().GetSort() == observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION {
		sort = "duration_nanos"
	}
	direction := " DESC"
	if req.GetSummary().GetAscending() {
		direction = " ASC"
	}
	query = "SELECT TraceId, root.1, root.2, root.3, start_time, duration_nanos, status, span_count FROM (" +
		query + ") ORDER BY " + sort + direction + ", TraceId ASC LIMIT ? OFFSET ?" + settings
	args = append(args, limit, req.GetSummary().GetOffset())
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
	return &observabilityv1.QueryTracesResponse{Summaries: summaries, TotalCount: total}, nil
}
