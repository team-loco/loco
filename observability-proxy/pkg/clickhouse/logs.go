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
	// KubernetesEvents selects the resource's Kubernetes events instead of its logs.
	KubernetesEvents bool
}

type logPage struct {
	entries    []*observabilityv1.LogEntry
	nextCursor string
	positions  []partPosition
}

func (c *Client) QueryLogs(ctx context.Context, q LogQuery) (*observabilityv1.QueryLogsResponse, error) {
	query, args, err := buildLogPageQuery(q, c.queryTimeout)
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

const logSelect = "SELECT Timestamp, SeverityText, Body, " + resourceColumn + ", TraceId, SpanId, " +
	"ResourceAttributes, LogAttributes, _block_number, _block_offset, _partition_id FROM otel_logs"

type logBinder interface {
	str(value string) string
	num(value int64) string
}

type positionalBinder struct{ args []any }

func (b *positionalBinder) add(value any) string {
	b.args = append(b.args, value)
	return "?"
}

func (b *positionalBinder) str(value string) string { return b.add(value) }

func (b *positionalBinder) num(value int64) string { return b.add(value) }

func buildLogPageQuery(q LogQuery, queryTimeout time.Duration) (string, []any, error) {
	b := &positionalBinder{}
	whereParts := logFilters(q, b)
	whereParts = append(whereParts, "Timestamp <= fromUnixTimestamp64Nano("+b.num(q.End.UnixNano())+")")

	if q.Cursor != "" {
		position, err := decodePartPosition(q.Cursor)
		if err != nil {
			return "", nil, ErrInvalidLogCursor
		}
		comparison := "<"
		if q.Order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
			comparison = ">"
		}
		whereParts = append(
			whereParts,
			fmt.Sprintf(
				"%s %s (fromUnixTimestamp64Nano(%s), %s, %s)",
				partPositionColumns,
				comparison,
				b.num(position.timestamp.UnixNano()),
				b.add(position.blockNumber),
				b.add(position.blockOffset),
			),
		)
	}

	order := "ORDER BY Timestamp DESC, _block_number DESC, _block_offset DESC"
	if q.Order == observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
		order = "ORDER BY Timestamp ASC, _block_number ASC, _block_offset ASC"
	}

	query := strings.Join([]string{
		logSelect,
		"WHERE " + strings.Join(whereParts, " AND "),
		order,
		"LIMIT " + b.num(int64(q.Limit)+pageLookahead),
		fmt.Sprintf("SETTINGS max_execution_time = %d", int(queryTimeout.Seconds())),
	}, " ")
	return query, b.args, nil
}

func logFilters(q LogQuery, b logBinder) []string {
	whereParts := []string{workspaceColumn + " = " + b.str(q.WorkspaceID)}

	if q.EnvironmentID != "" {
		whereParts = append(whereParts, environmentColumn+" = "+b.str(q.EnvironmentID))
	}

	if len(q.ResourceIDs) > 0 {
		whereParts = append(whereParts, logIn(resourceColumn, q.ResourceIDs, b))
	}

	whereParts = append(whereParts, "Timestamp >= fromUnixTimestamp64Nano("+b.num(q.Start.UnixNano())+")")

	scope := "ResourceAttributes[" + b.str(telemetryScopeAttribute) + "]"
	if q.KubernetesEvents {
		whereParts = append(whereParts, scope+" = "+b.str(kubernetesEventScope))
	} else {
		whereParts = append(whereParts, scope+" != "+b.str(kubernetesEventScope))
	}

	if len(q.Levels) > 0 {
		whereParts = append(whereParts, logIn("SeverityText", q.Levels, b))
	}

	if q.Search != "" {
		whereParts = append(whereParts, "position(Body, "+b.str(q.Search)+") > 0")
	}

	for k, v := range q.Labels {
		whereParts = append(whereParts, "ResourceAttributes["+b.str(k)+"] = "+b.str(v))
	}

	for _, group := range []struct {
		values  []string
		exclude bool
	}{
		{q.Regions, false}, {q.ExcludedRegions, true},
	} {
		var clauses []string
		for _, pattern := range group.values {
			attribute := "ResourceAttributes[" + b.str("loco.region") + "]"
			value := b.str(strings.ToLower(strings.TrimSuffix(pattern, "*")))
			if strings.HasSuffix(pattern, "*") {
				clauses = append(clauses, "startsWith(lowerUTF8("+attribute+"), "+value+")")
			} else {
				clauses = append(clauses, "lowerUTF8("+attribute+") = "+value)
			}
		}
		if len(clauses) > 0 {
			clause := "(" + strings.Join(clauses, " OR ") + ")"
			if group.exclude {
				clause = "NOT " + clause
			}
			whereParts = append(whereParts, clause)
		}
	}

	return whereParts
}

func logIn(column string, values []string, b logBinder) string {
	placeholders := make([]string, len(values))
	for i, value := range values {
		placeholders[i] = b.str(value)
	}
	return fmt.Sprintf("%s IN (%s)", column, strings.Join(placeholders, ","))
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
