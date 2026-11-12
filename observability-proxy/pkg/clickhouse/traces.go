package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func traceScope(scope *observabilityv1.TraceScope) ([]string, []any) {
	where := []string{workspaceColumn + " = ?"}
	args := []any{scope.GetWorkspaceId()}
	if len(scope.GetResourceIds()) > 0 {
		clause, values := inFilter(resourceColumn, scope.GetResourceIds())
		where = append(where, clause)
		args = append(args, values...)
	}
	if scope.GetEnvironmentId() != "" {
		where = append(where, environmentColumn+" = ?")
		args = append(args, scope.GetEnvironmentId())
	}
	return where, args
}

func (c *Client) GetTrace(
	ctx context.Context,
	req *observabilityv1.GetTraceRequest,
) (*observabilityv1.GetTraceResponse, error) {
	lookup, query, args := buildGetTraceQueries(req, c.queryTimeout)
	var count uint64
	var start, end time.Time
	if err := c.conn.QueryRow(ctx, lookup, args...).Scan(&count, &start, &end); err != nil {
		return nil, fmt.Errorf("look up trace timestamps: %w", err)
	}
	if count == 0 {
		return &observabilityv1.GetTraceResponse{}, nil
	}
	// The lookup stores whole seconds, so include the fractional part of its final second.
	args = append(args, start.UnixNano(), end.Add(time.Second).UnixNano())
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse trace query: %w", err)
	}
	defer rows.Close()
	response := &observabilityv1.GetTraceResponse{}
	for rows.Next() {
		span := &observabilityv1.TraceSpan{}
		var timestamp time.Time
		var eventTimes []time.Time
		var eventNames []string
		var eventAttributes []map[string]string
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
			&eventTimes,
			&eventNames,
			&eventAttributes,
		); err != nil {
			return nil, fmt.Errorf("scan trace span: %w", err)
		}
		span.StartTime = timestamppb.New(timestamp)
		span.Events = spanEvents(eventTimes, eventNames, eventAttributes)
		response.Spans = append(response.Spans, span)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trace rows iteration: %w", err)
	}
	return response, nil
}

func buildGetTraceQueries(req *observabilityv1.GetTraceRequest, timeout time.Duration) (string, string, []any) {
	where, args := traceScope(req.GetScope())
	where = append(where, "TraceId = ?")
	args = append(args, req.GetTraceId())
	predicates := strings.Join(where, " AND ")
	settings := fmt.Sprintf(" SETTINGS max_execution_time = %d", int(timeout.Seconds()))
	lookup := "SELECT count(), min(Start), max(End) FROM otel_traces_trace_id_ts WHERE " + predicates + settings
	query := "SELECT Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, " +
		"Duration, StatusCode, StatusMessage, WorkspaceId, EnvironmentId, ResourceId, ResourceAttributes, " +
		"SpanAttributes, ScopeName, ScopeVersion, Events.Timestamp, Events.Name, Events.Attributes " +
		"FROM otel_traces WHERE " + predicates +
		" AND Timestamp >= fromUnixTimestamp64Nano(?) AND Timestamp < fromUnixTimestamp64Nano(?)" +
		" ORDER BY Timestamp ASC, SpanId ASC" + settings
	return lookup, query, args
}

// spanEvents zips the Events nested columns, whose arrays ClickHouse keeps equal in length.
func spanEvents(times []time.Time, names []string, attributes []map[string]string) []*observabilityv1.TraceSpanEvent {
	events := make([]*observabilityv1.TraceSpanEvent, len(names))
	for i, name := range names {
		events[i] = &observabilityv1.TraceSpanEvent{
			Name:       name,
			Timestamp:  timestamppb.New(times[i]),
			Attributes: attributes[i],
		}
	}
	return events
}
