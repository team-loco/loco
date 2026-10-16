package clickhouse

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	workspaceColumn    = "WorkspaceId"
	environmentColumn  = "EnvironmentId"
	resourceColumn     = "ResourceId"
	pageLookahead      = 1
	cursorParts        = 3
	cursorIntegerBits  = 64
	logPositionColumns = "(Timestamp, _block_number, _block_offset)"
)

var ErrInvalidLogCursor = errors.New("invalid log cursor")

type LogPage struct {
	Entries    []*observabilityv1.LogEntry
	NextCursor string
	LastCursor string
}

type logCursor struct {
	timestamp   time.Time
	blockNumber uint64
	blockOffset uint64
}

func (cursor logCursor) encode() string {
	timestamp := cursor.timestamp.UTC().Format(time.RFC3339Nano)
	value := fmt.Sprintf("%s/%d/%d", timestamp, cursor.blockNumber, cursor.blockOffset)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeLogCursor(value string) (logCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return logCursor{}, ErrInvalidLogCursor
	}
	parts := strings.Split(string(decoded), "/")
	if len(parts) != cursorParts {
		return logCursor{}, ErrInvalidLogCursor
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || !time.Unix(0, timestamp.UnixNano()).Equal(timestamp) {
		return logCursor{}, ErrInvalidLogCursor
	}
	blockNumber, err := strconv.ParseUint(parts[1], 10, cursorIntegerBits)
	if err != nil {
		return logCursor{}, ErrInvalidLogCursor
	}
	blockOffset, err := strconv.ParseUint(parts[2], 10, cursorIntegerBits)
	if err != nil {
		return logCursor{}, ErrInvalidLogCursor
	}
	return logCursor{timestamp: timestamp, blockNumber: blockNumber, blockOffset: blockOffset}, nil
}

type LogRegions struct {
	Include []string
	Exclude []string
}

func QueryLogs(ctx context.Context, conn driver.Conn, workspaceID string, resourceIDs []string,
	startTime, endTime time.Time, search string, levels []string, labels map[string]string,
	limit int32, cursor string, order observabilityv1.LogOrder, queryTimeout int, regions LogRegions,
) ([]*observabilityv1.LogEntry, string, error) {
	page, err := QueryLogPage(
		ctx,
		conn,
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
		queryTimeout, regions,
	)
	if err != nil {
		return nil, "", err
	}
	return page.Entries, page.NextCursor, nil
}

func QueryLogPage(
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
	queryTimeout int, regions LogRegions,
) (LogPage, error) {
	query, args, err := buildLogsQuery(
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
		queryTimeout, regions,
	)
	if err != nil {
		return LogPage{}, err
	}
	slog.Debug("executing log query", "query", query)

	rows, err := conn.Query(ctx, query, args...)
	if err != nil {
		return LogPage{}, fmt.Errorf("clickhouse query: %w", err)
	}
	defer rows.Close()

	var entries []*observabilityv1.LogEntry
	var positions []logCursor

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
		); err != nil {
			return LogPage{}, fmt.Errorf("scan row: %w", err)
		}

		positions = append(positions, logCursor{timestamp: ts, blockNumber: blockNumber, blockOffset: blockOffset})
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
		return LogPage{}, fmt.Errorf("rows iteration: %w", err)
	}

	page := LogPage{Entries: entries}
	if int32(len(entries)) > limit {
		page.Entries = entries[:limit]
		page.NextCursor = positions[limit-1].encode()
	}
	if len(page.Entries) > 0 {
		page.LastCursor = positions[len(page.Entries)-1].encode()
	}
	return page, nil
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
	queryTimeout int, regions LogRegions,
) (string, []any, error) {
	var args []any
	queryParts := []string{
		"SELECT Timestamp, SeverityText, Body, " + resourceColumn + ", TraceId, SpanId, " +
			"ResourceAttributes, LogAttributes, _block_number, _block_offset FROM otel_logs",
	}

	whereParts := []string{workspaceColumn + " = ?"}
	args = append(args, workspaceID)

	if len(resourceIDs) > 0 {
		clause, values := inFilter(resourceColumn, resourceIDs)
		whereParts = append(whereParts, clause)
		args = append(args, values...)
	}

	whereParts = append(
		whereParts,
		"Timestamp >= fromUnixTimestamp64Nano(?)",
		"Timestamp <= fromUnixTimestamp64Nano(?)",
	)
	args = append(args, startTime.UnixNano(), endTime.UnixNano())

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

	for _, group := range []struct {
		values  []string
		exclude bool
	}{
		{regions.Include, false}, {regions.Exclude, true},
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

	if cursor != "" {
		position, err := decodeLogCursor(cursor)
		if err != nil {
			return "", nil, err
		}
		if order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
			whereParts = append(whereParts, logPositionColumns+" > (fromUnixTimestamp64Nano(?), ?, ?)")
		} else {
			whereParts = append(whereParts, logPositionColumns+" < (fromUnixTimestamp64Nano(?), ?, ?)")
		}
		args = append(args, position.timestamp.UnixNano(), position.blockNumber, position.blockOffset)
	}

	queryParts = append(queryParts, "WHERE "+strings.Join(whereParts, " AND "))

	if order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
		queryParts = append(queryParts, "ORDER BY Timestamp ASC, _block_number ASC, _block_offset ASC")
	} else {
		queryParts = append(queryParts, "ORDER BY Timestamp DESC, _block_number DESC, _block_offset DESC")
	}

	queryParts = append(queryParts, "LIMIT ?")
	args = append(args, limit+pageLookahead)

	settingsQuery := fmt.Sprintf("SETTINGS max_execution_time = %d", queryTimeout)
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
