package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	tailBlockKey  = "concat(_partition_id, '/', toString(_block_number))"
	tailSeenParam = "tail_seen"
	tailSelect    = "WITH JSONExtractKeysAndValues({" + tailSeenParam + ":String}, 'UInt64') " +
		"AS tail_positions " + logSelect
	tailJoin = "LEFT JOIN (SELECT position.1 AS tail_block, position.2 AS tail_offset, 1 AS tail_found " +
		"FROM (SELECT arrayJoin(tail_positions) AS position) AS block_rows) AS delivered ON " +
		tailBlockKey + " = tail_block"
	tailNotDelivered = "(coalesce(tail_found, 0) = 0 OR _block_offset > tail_offset)"
	tailOrder        = "ORDER BY _partition_id ASC, _block_number ASC, _block_offset ASC"
)

type TailLogCursor map[string]uint64

type TailLogPage struct {
	Entries []*observabilityv1.LogEntry
	Blocks  TailLogCursor
	More    bool
}

type namedBinder struct{ parameters ch.Parameters }

func (b namedBinder) add(value, format string) string {
	name := fmt.Sprintf("tail_arg_%d", len(b.parameters))
	b.parameters[name] = value
	return fmt.Sprintf(format, name)
}

func (b namedBinder) str(value string) string { return b.add(value, "{%s:String}") }

func (b namedBinder) num(value int64) string {
	return b.add(strconv.FormatInt(value, 10), "{%s:Int64}")
}

func buildTailLogQuery(q LogQuery, queryTimeout time.Duration, seen TailLogCursor) (string, ch.Parameters, error) {
	data, err := json.Marshal(seen)
	if err != nil {
		return "", nil, fmt.Errorf("encode tail positions: %w", err)
	}
	b := namedBinder{parameters: ch.Parameters{tailSeenParam: string(data)}}
	whereParts := append(logFilters(q, b), tailNotDelivered)
	query := strings.Join([]string{
		tailSelect,
		tailJoin,
		"WHERE " + strings.Join(whereParts, " AND "),
		tailOrder,
		"LIMIT " + b.num(int64(q.Limit)+pageLookahead),
		fmt.Sprintf("SETTINGS max_execution_time = %d, join_algorithm = 'hash'", int(queryTimeout.Seconds())),
	}, " ")
	return query, b.parameters, nil
}

func (c *Client) QueryTailLogPage(ctx context.Context, q LogQuery, cursor TailLogCursor) (TailLogPage, error) {
	query, parameters, err := buildTailLogQuery(q, c.queryTimeout, cursor)
	if err != nil {
		return TailLogPage{}, err
	}
	ctx = ch.Context(ctx, ch.WithParameters(parameters))
	page, err := queryLogPage(ctx, c.conn, query, nil, q.Limit)
	if err != nil {
		return TailLogPage{}, err
	}
	blocks := make(TailLogCursor)
	for _, position := range page.positions {
		key := position.partitionID + "/" + strconv.FormatUint(position.blockNumber, 10)
		blocks[key] = position.blockOffset
	}
	return TailLogPage{Entries: page.entries, Blocks: blocks, More: page.nextCursor != ""}, nil
}
