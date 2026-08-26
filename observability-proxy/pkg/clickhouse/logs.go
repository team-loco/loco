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
	workspaceAttr = "ResourceAttributes['k8s.pod.labels.loco.io/workspace-id']"
	resourceAttr  = "ResourceAttributes['k8s.pod.labels.loco.io/resource-id']"
)

// QueryLogs executes a parameterized log query against the otel_logs table.
// Mandatory filters (workspace, resources) are always injected and cannot be overridden by user input.
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
	// Build query with mandatory filters
	var queryParts []string
	var args []any

	queryParts = append(queryParts, fmt.Sprintf("SELECT Timestamp, SeverityText, Body, %s AS resource_id, TraceId, SpanId, ResourceAttributes, LogAttributes FROM otel_logs", resourceAttr))

	// Mandatory WHERE clauses - these are NEVER user-controlled
	whereParts := []string{
		fmt.Sprintf("%s = ?", workspaceAttr),
	}
	args = append(args, workspaceID)

	if len(resourceIDs) > 0 {
		placeholders := make([]string, len(resourceIDs))
		for i, rid := range resourceIDs {
			placeholders[i] = "?"
			args = append(args, rid)
		}
		whereParts = append(whereParts, fmt.Sprintf("%s IN (%s)", resourceAttr, strings.Join(placeholders, ",")))
	}

	// Time range
	whereParts = append(whereParts, "Timestamp >= ?", "Timestamp <= ?")
	args = append(args, startTime, endTime)

	// Optional: severity levels
	if len(levels) > 0 {
		placeholders := make([]string, len(levels))
		for i, l := range levels {
			placeholders[i] = "?"
			args = append(args, l)
		}
		whereParts = append(whereParts, fmt.Sprintf("SeverityText IN (%s)", strings.Join(placeholders, ",")))
	}

	// Optional: full-text search (LIKE-based, safe with parameterized query)
	if search != "" {
		whereParts = append(whereParts, "Body LIKE ?")
		args = append(args, "%"+search+"%")
	}

	// Optional: label filters on resource attributes
	for k, v := range labels {
		whereParts = append(whereParts, "ResourceAttributes[?] = ?")
		args = append(args, k, v)
	}

	// Cursor-based pagination (cursor is a timestamp)
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

	// Order
	if order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
		queryParts = append(queryParts, "ORDER BY Timestamp ASC")
	} else {
		queryParts = append(queryParts, "ORDER BY Timestamp DESC")
	}

	// Limit (fetch one extra to determine if there's a next page)
	queryParts = append(queryParts, "LIMIT ?")
	args = append(args, limit+1)

	// Set query timeout
	settingsQuery := fmt.Sprintf("SETTINGS max_execution_time = %d", queryTimeout)
	queryParts = append(queryParts, settingsQuery)

	query := strings.Join(queryParts, " ")
	slog.Debug("executing log query", "query", query)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var entries []*observabilityv1.LogEntry
	var lastTimestamp time.Time

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

		if err := rows.Scan(&ts, &severity, &body, &resourceID, &traceID, &spanID, &resourceAttrs, &logAttrs); err != nil {
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
		lastTimestamp = ts
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("rows iteration: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if int32(len(entries)) > limit {
		entries = entries[:limit]
		nextCursor = lastTimestamp.Format(time.RFC3339Nano)
	}

	return entries, nextCursor, nil
}
