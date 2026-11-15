package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// traceFilters holds the fields ListTracesRequest and QueryTraceBucketsRequest share.
type traceFilters interface {
	proto.Message
	GetScope() *observabilityv1.TraceScope
	GetStartTime() *timestamppb.Timestamp
	GetEndTime() *timestamppb.Timestamp
	GetTraceId() string
	GetMinDurationNanos() uint64
	GetMaxDurationNanos() uint64
	GetStatus() observabilityv1.TraceStatus
	GetRootNameTerms() []string
}

func buildTraceSummaryQuery(req traceFilters) (string, []any) {
	where, args := traceScope(req.GetScope())
	where = append(where, "Timestamp >= fromUnixTimestamp64Nano(?)", "Timestamp <= fromUnixTimestamp64Nano(?)")
	args = append(args, req.GetStartTime().AsTime().UnixNano(), req.GetEndTime().AsTime().UnixNano())
	if req.GetTraceId() != "" {
		where = append(where, "TraceId = ?")
		args = append(args, req.GetTraceId())
	}
	having := []string{"1"}
	if hasField(req, "min_duration_nanos") {
		having = append(having, "duration_nanos >= ?")
		args = append(args, req.GetMinDurationNanos())
	}
	if hasField(req, "max_duration_nanos") {
		having = append(having, "duration_nanos <= ?")
		args = append(args, req.GetMaxDurationNanos())
	}
	if req.GetStatus() != observabilityv1.TraceStatus_TRACE_STATUS_UNSPECIFIED {
		having = append(having, "status = ?")
		args = append(args, int32(req.GetStatus()))
	}
	for _, term := range req.GetRootNameTerms() {
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

func hasField(message proto.Message, name protoreflect.Name) bool {
	m := message.ProtoReflect()
	return m.Has(m.Descriptor().Fields().ByName(name))
}

func (c *Client) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
	limit int32,
) (*observabilityv1.ListTracesResponse, error) {
	query, args := buildTraceSummaryQuery(req)
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
