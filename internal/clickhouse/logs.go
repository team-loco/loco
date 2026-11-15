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

// LogQuery selects log rows. Each Excluded field leaves out the rows its include counterpart would select.
type LogQuery struct {
	WorkspaceID           string
	EnvironmentID         string
	ResourceIDs           []string
	ExcludedResourceIDs   []string
	Start                 time.Time
	End                   time.Time
	Search                string
	Levels                []string
	ExcludedLevels        []string
	Labels                []*observabilityv1.LogFieldFilter
	ExcludedLabels        []*observabilityv1.LogFieldFilter
	LogAttributes         []*observabilityv1.LogFieldFilter
	ExcludedLogAttributes []*observabilityv1.LogFieldFilter
	JSONFields            []*observabilityv1.LogFieldFilter
	ExcludedJSONFields    []*observabilityv1.LogFieldFilter
	TraceIDs              []string
	ExcludedTraceIDs      []string
	Regions               []string
	ExcludedRegions       []string
	Limit                 int32
	Cursor                string
	Order                 observabilityv1.LogOrder
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

	whereParts = append(whereParts, "Timestamp >= fromUnixTimestamp64Nano("+b.num(q.Start.UnixNano())+")")

	for _, filter := range []struct {
		column  string
		values  []string
		exclude bool
	}{
		{resourceColumn, q.ResourceIDs, false}, {resourceColumn, q.ExcludedResourceIDs, true},
		{"SeverityText", q.Levels, false}, {"SeverityText", q.ExcludedLevels, true},
		{"TraceId", q.TraceIDs, false}, {"TraceId", q.ExcludedTraceIDs, true},
	} {
		if len(filter.values) > 0 {
			whereParts = append(whereParts, logIn(filter.column, filter.values, filter.exclude, b))
		}
	}

	if q.Search != "" {
		whereParts = append(whereParts, "position(Body, "+b.str(q.Search)+") > 0")
	}

	resourceAttribute := func(key string) string { return "ResourceAttributes[" + b.str(key) + "]" }
	logAttribute := func(key string) string { return "LogAttributes[" + b.str(key) + "]" }
	jsonField := func(key string) string { return jsonBodyField(key, b) }
	for _, group := range []struct {
		field   func(key string) string
		filters []*observabilityv1.LogFieldFilter
		exclude bool
	}{
		{resourceAttribute, q.Labels, false}, {resourceAttribute, q.ExcludedLabels, true},
		{logAttribute, q.LogAttributes, false}, {logAttribute, q.ExcludedLogAttributes, true},
		{jsonField, q.JSONFields, false}, {jsonField, q.ExcludedJSONFields, true},
	} {
		for _, filter := range group.filters {
			whereParts = append(whereParts, logFieldMatch(group.field, filter, group.exclude, b))
		}
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

// logFieldMatch renders field once per comparison, since each rendering binds its own parameters.
func logFieldMatch(
	field func(key string) string,
	filter *observabilityv1.LogFieldFilter,
	exclude bool,
	b logBinder,
) string {
	var clauses []string
	if len(filter.GetValues()) > 0 {
		clauses = append(clauses, logIn(field(filter.GetKey()), filter.GetValues(), false, b))
	}
	for _, prefix := range filter.GetPrefixes() {
		clauses = append(clauses, "startsWith("+field(filter.GetKey())+", "+b.str(prefix)+")")
	}
	clause := "(" + strings.Join(clauses, " OR ") + ")"
	if exclude {
		clause = "NOT " + clause
	}
	return clause
}

// jsonBodyField reads a dot-separated object path from a JSON body, a string as its text and anything else as JSON.
func jsonBodyField(key string, b logBinder) string {
	path := func() string {
		segments := strings.Split(key, ".")
		placeholders := make([]string, len(segments))
		for i, segment := range segments {
			placeholders[i] = b.str(segment)
		}
		return strings.Join(placeholders, ", ")
	}
	return "if(JSONType(Body, " + path() + ") = 'String', JSONExtractString(Body, " + path() +
		"), JSONExtractRaw(Body, " + path() + "))"
}

func logIn(column string, values []string, exclude bool, b logBinder) string {
	placeholders := make([]string, len(values))
	for i, value := range values {
		placeholders[i] = b.str(value)
	}
	operator := "IN"
	if exclude {
		operator = "NOT IN"
	}
	return fmt.Sprintf("%s %s (%s)", column, operator, strings.Join(placeholders, ","))
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
