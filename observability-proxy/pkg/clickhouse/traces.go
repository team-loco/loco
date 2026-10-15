package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

var ErrInvalidTraceCursor = errors.New("invalid trace cursor")

func buildTracesQuery(req *observabilityv1.QueryTracesRequest, limit int32, queryTimeout int) (string, []any, error) {
	where := []string{
		workspaceColumn + " = ?",
		"Timestamp >= fromUnixTimestamp64Nano(?)",
		"Timestamp <= fromUnixTimestamp64Nano(?)",
	}
	args := []any{req.GetWorkspaceId(), req.GetStartTime().AsTime().UnixNano(), req.GetEndTime().AsTime().UnixNano()}
	if len(req.GetResourceIds()) > 0 {
		clause, values := inFilter(resourceColumn, req.GetResourceIds())
		where = append(where, clause)
		args = append(args, values...)
	}
	if req.GetEnvironmentId() != "" {
		where = append(where, environmentColumn+" = ?")
		args = append(args, req.GetEnvironmentId())
	}
	if req.GetTraceId() != "" {
		where = append(where, "TraceId = ?")
		args = append(args, req.GetTraceId())
	}
	if req.GetCursor() != "" {
		position, err := decodeLogCursor(req.GetCursor())
		if err != nil {
			return "", nil, ErrInvalidTraceCursor
		}
		where = append(where, logPositionColumns+" > (fromUnixTimestamp64Nano(?), ?, ?)")
		args = append(args, position.timestamp.UnixNano(), position.blockNumber, position.blockOffset)
	}
	args = append(args, limit+pageLookahead)
	query := "SELECT Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, " +
		"Duration, StatusCode, StatusMessage, WorkspaceId, EnvironmentId, ResourceId, ResourceAttributes, " +
		"SpanAttributes, ScopeName, ScopeVersion, _block_number, _block_offset FROM otel_traces WHERE " + strings.Join(
		where,
		" AND ",
	) + " ORDER BY Timestamp ASC, _block_number ASC, _block_offset ASC LIMIT ?"
	return fmt.Sprintf("%s SETTINGS max_execution_time = %d", query, queryTimeout), args, nil
}

func QueryTraces(
	ctx context.Context,
	conn driver.Conn,
	req *observabilityv1.QueryTracesRequest,
	limit int32,
	queryTimeout int,
) (*observabilityv1.QueryTracesResponse, error) {
	query, args, err := buildTracesQuery(req, limit, queryTimeout)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse trace query: %w", err)
	}
	defer rows.Close()
	response := &observabilityv1.QueryTracesResponse{}
	var last logCursor
	for rows.Next() {
		span := &observabilityv1.TraceSpan{}
		var timestamp time.Time
		var position logCursor
		if err := rows.Scan(
			&timestamp,
			&span.TraceId,
			&span.SpanId,
			&span.ParentSpanId,
			&span.Name,
			&span.Kind,
			&span.ServiceName,
			&span.DurationNanos,
			&span.StatusCode,
			&span.StatusMessage,
			&span.WorkspaceId,
			&span.EnvironmentId,
			&span.ResourceId,
			&span.ResourceAttributes,
			&span.SpanAttributes,
			&span.ScopeName,
			&span.ScopeVersion,
			&position.blockNumber,
			&position.blockOffset,
		); err != nil {
			return nil, fmt.Errorf("scan trace span: %w", err)
		}
		if int32(len(response.GetSpans())) == limit {
			response.NextCursor = last.encode()
			break
		}
		span.StartTime = timestamppb.New(timestamp)
		response.Spans = append(response.Spans, span)
		position.timestamp = timestamp
		last = position
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trace rows iteration: %w", err)
	}
	return response, nil
}
