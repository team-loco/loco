package clickhouse

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	workspaceColumn = "WorkspaceId"
	resourceColumn  = "ResourceId"
	pageLookahead   = 1
)

// QueryLogs executes a parameterized log query against the otel_logs table.
// The workspace filter is always applied and cannot be overridden by user input.
func QueryLogs(
	ctx context.Context,
	conn driver.Conn,
	workspaceID string,
	resourceIDs []string,
	startTime, endTime time.Time,
	search string,
	levels []string,
	labels map[string]string,
	limit int32,
	cursor string,
	order observabilityv1.LogOrder,
	queryTimeout int,
) ([]*observabilityv1.LogEntry, string, error) {
	query, args := buildLogsQuery(
		workspaceID,
		resourceIDs,
		startTime,
		endTime,
		search,
		levels,
		labels,
		limit,
		cursor,
		order,
		queryTimeout,
	)
	slog.Debug("executing log query", "query", query)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var entries []*observabilityv1.LogEntry

	for rows.Next() {
		var (
			ts            time.Time
			severity      string
			body          string
			resourceID    string
			traceID       string
			spanID        string
			resourceAttrs map[string]string
			logAttrs      map[string]string
		)

		if err := rows.Scan(
			&ts,
			&severity,
			&body,
			&resourceID,
			&traceID,
			&spanID,
			&resourceAttrs,
			&logAttrs,
		); err != nil {
			return nil, "", fmt.Errorf("scan row: %w", err)
		}

		entries = append(entries, &observabilityv1.LogEntry{
			Timestamp:          timestamppb.New(ts),
			Severity:           severity,
			Body:               body,
			ResourceId:         resourceID,
			TraceId:            traceID,
			SpanId:             spanID,
			ResourceAttributes: resourceAttrs,
			LogAttributes:      logAttrs,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("rows iteration: %w", err)
	}

	var nextCursor string
	if int32(len(entries)) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1].GetTimestamp().AsTime()
		nextCursor = last.Format(time.RFC3339Nano)
	}

	return entries, nextCursor, nil
}

func buildLogsQuery(
	workspaceID string,
	resourceIDs []string,
	startTime, endTime time.Time,
	search string,
	levels []string,
	labels map[string]string,
	limit int32,
	cursor string,
	order observabilityv1.LogOrder,
	queryTimeout int,
) (string, []any) {
	var args []any
	queryParts := []string{
		"SELECT Timestamp, SeverityText, Body, " + resourceColumn + ", TraceId, SpanId, " +
			"ResourceAttributes, LogAttributes FROM otel_logs",
	}

	whereParts := []string{workspaceColumn + " = ?"}
	args = append(args, workspaceID)

	if len(resourceIDs) > 0 {
		clause, values := inFilter(resourceColumn, resourceIDs)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	whereParts = append(whereParts, "Timestamp >= ?", "Timestamp <= ?")
	args = append(args, startTime, endTime)

	if len(levels) > 0 {
		clause, values := inFilter("SeverityText", levels)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	if search != "" {
		whereParts = append(whereParts, "Body LIKE ?")
		args = append(args, "%"+search+"%")
	}

	for k, v := range labels {
		whereParts = append(whereParts, "ResourceAttributes[?] = ?")
		args = append(args, k, v)
	}

	if cursor != "" {
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor)
		if err == nil {
			if order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
				whereParts = append(whereParts, "Timestamp > ?")
			} else {
				whereParts = append(whereParts, "Timestamp < ?")
			}
			args = append(args, cursorTime)
		}
	}

	queryParts = append(queryParts, "WHERE "+strings.Join(whereParts, " AND "))

	if order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
		queryParts = append(queryParts, "ORDER BY Timestamp ASC")
	} else {
		queryParts = append(queryParts, "ORDER BY Timestamp DESC")
	}

	queryParts = append(queryParts, "LIMIT ?")
	args = append(args, limit+pageLookahead)

	settingsQuery := fmt.Sprintf("SETTINGS max_execution_time = %d", queryTimeout)
	queryParts = append(queryParts, settingsQuery)

	return strings.Join(queryParts, " "), args
}

func inFilter(column string, values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, value := range values {
		placeholders[i] = "?"
		args[i] = value
	}
	return fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ",")), args
}
