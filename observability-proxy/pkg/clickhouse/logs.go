package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	workspaceColumn   = "WorkspaceId"
	environmentColumn = "EnvironmentId"
	resourceColumn    = "ResourceId"
	pageLookahead     = 1
)

var ErrInvalidLogCursor = errors.New("invalid log cursor")

type LogQuery struct {
	WorkspaceID     string
	EnvironmentID   string
	ResourceIDs     []string
	Start           time.Time
	End             time.Time
	Search          string
	Levels          []string
	Labels          map[string]string
	Regions         []string
	ExcludedRegions []string
	Limit           int32
	Cursor          string
	Order           observabilityv1.LogOrder
}

type logPage struct {
	entries    []*observabilityv1.LogEntry
	nextCursor string
	positions  []partPosition
}

func (c *Client) QueryLogs(ctx context.Context, q LogQuery) (*observabilityv1.QueryLogsResponse, error) {
	query, args, err := buildLogPageQuery(q, c.queryTimeout, false)
	if err != nil {
		return nil, err
	}
	slog.DebugContext(ctx, "executing log query", "query", query)
	page, err := queryLogPage(ctx, c.conn, query, args, q.Limit)
	if err != nil {
		return nil, err
	}
	return &observabilityv1.QueryLogsResponse{Entries: page.entries, NextCursor: page.nextCursor}, nil
}

func queryLogPage(ctx context.Context, conn driver.Conn, query string, args []any, limit int32) (logPage, error) {
	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return logPage{}, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var entries []*observabilityv1.LogEntry
	var positions []partPosition

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
			blockNumber   uint64
			blockOffset   uint64
			partitionID   string
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
			&blockNumber,
			&blockOffset,
			&partitionID,
		); err != nil {
			return logPage{}, fmt.Errorf("scan row: %w", err)
		}

		positions = append(
			positions,
			partPosition{timestamp: ts, blockNumber: blockNumber, blockOffset: blockOffset, partitionID: partitionID},
		)
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
		return logPage{}, fmt.Errorf("rows iteration: %w", err)
	}

	page := logPage{entries: entries, positions: positions}
	if int32(len(entries)) > limit {
		page.entries = entries[:limit]
		page.positions = positions[:limit]
		page.nextCursor = positions[limit-1].encode()
	}
	return page, nil
}

func buildLogPageQuery(q LogQuery, queryTimeout int, tail bool) (string, []any, error) {
	var args []any
	queryParts := []string{
		"SELECT Timestamp, SeverityText, Body, " + resourceColumn + ", TraceId, SpanId, " +
			"ResourceAttributes, LogAttributes, _block_number, _block_offset, _partition_id FROM otel_logs",
	}

	if tail {
		queryParts[0] = "WITH JSONExtractKeysAndValues({tail_seen:String}, 'UInt64') AS tail_positions " +
			queryParts[0] + " LEFT JOIN (SELECT position.1 AS tail_block, position.2 AS tail_offset, 1 AS tail_found " +
			"FROM (SELECT arrayJoin(tail_positions) AS position) AS block_rows) AS delivered ON " +
			tailBlockKey + " = tail_block"
	}

	whereParts := []string{workspaceColumn + " = ?"}
	args = append(args, q.WorkspaceID)

	if q.EnvironmentID != "" {
		whereParts = append(whereParts, environmentColumn+" = ?")
		args = append(args, q.EnvironmentID)
	}

	if len(q.ResourceIDs) > 0 {
		clause, values := inFilter(resourceColumn, q.ResourceIDs)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	whereParts = append(whereParts, "Timestamp >= fromUnixTimestamp64Nano(?)")
	args = append(args, q.Start.UnixNano())
	if !tail {
		whereParts = append(whereParts, "Timestamp <= fromUnixTimestamp64Nano(?)")
		args = append(args, q.End.UnixNano())
	}

	if len(q.Levels) > 0 {
		clause, values := inFilter("SeverityText", q.Levels)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	if q.Search != "" {
		whereParts = append(whereParts, "position(Body, ?) > 0")
		args = append(args, q.Search)
	}

	for k, v := range q.Labels {
		whereParts = append(whereParts, "ResourceAttributes[?] = ?")
		args = append(args, k, v)
	}

	for _, group := range []struct {
		values  []string
		exclude bool
	}{
		{q.Regions, false}, {q.ExcludedRegions, true},
	} {
		var clauses []string
		for _, pattern := range group.values {
			if strings.HasSuffix(pattern, "*") {
				clauses = append(clauses, "startsWith(lowerUTF8(ResourceAttributes[?]), ?)")
			} else {
				clauses = append(clauses, "lowerUTF8(ResourceAttributes[?]) = ?")
			}
			args = append(args, "loco.region", strings.ToLower(strings.TrimSuffix(pattern, "*")))
		}
		if len(clauses) > 0 {
			clause := "(" + strings.Join(clauses, " OR ") + ")"
			if group.exclude {
				clause = "NOT " + clause
			}
			whereParts = append(whereParts, clause)
		}
	}

	if tail {
		whereParts = append(
			whereParts,
			"(coalesce(tail_found, 0) = 0 OR _block_offset > tail_offset)",
		)
	}

	if q.Cursor != "" {
		position, err := decodePartPosition(q.Cursor)
		if err != nil {
			return "", nil, ErrInvalidLogCursor
		}
		if q.Order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
			whereParts = append(whereParts, partPositionColumns+" > (fromUnixTimestamp64Nano(?), ?, ?)")
		} else {
			whereParts = append(whereParts, partPositionColumns+" < (fromUnixTimestamp64Nano(?), ?, ?)")
		}
		args = append(args, position.timestamp.UnixNano(), position.blockNumber, position.blockOffset)
	}

	queryParts = append(queryParts, "WHERE "+strings.Join(whereParts, " AND "))

	switch {
	case tail:
		queryParts = append(queryParts, "ORDER BY _partition_id ASC, _block_number ASC, _block_offset ASC")
	case q.Order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST:
		queryParts = append(queryParts, "ORDER BY Timestamp ASC, _block_number ASC, _block_offset ASC")
	default:
		queryParts = append(queryParts, "ORDER BY Timestamp DESC, _block_number DESC, _block_offset DESC")
	}

	queryParts = append(queryParts, "LIMIT ?")
	args = append(args, q.Limit+pageLookahead)

	settingsQuery := fmt.Sprintf("SETTINGS max_execution_time = %d", queryTimeout)
	if tail {
		settingsQuery += ", join_algorithm = 'hash'"
	}
	queryParts = append(queryParts, settingsQuery)

	return strings.Join(queryParts, " "), args, nil
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
