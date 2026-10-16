package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

var errTailQueryParameter = errors.New("unsupported tail query parameter")

const tailBlockKey = "concat(_partition_id, '/', toString(_block_number))"

type TailLogCursor map[string]uint64

type TailLogPage struct {
	Entries []*observabilityv1.LogEntry
	Blocks  TailLogCursor
	More    bool
}

func QueryTailLogPage(
	ctx context.Context,
	conn driver.Conn,
	request *observabilityv1.TailLogsRequest,
	since time.Time,
	limit int32,
	queryTimeout int,
	cursor TailLogCursor,
) (TailLogPage, error) {
	query, args, err := buildLogPageQuery(
		request.GetWorkspaceId(),
		request.GetResourceIds(),
		since,
		time.Time{},
		request.GetSearch(),
		request.GetLevels(),
		request.GetLabels(),
		limit,
		"",
		observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
		queryTimeout,
		LogScope{
			EnvironmentID: request.GetEnvironmentId(),
			Include:       request.GetRegions(),
			Exclude:       request.GetExcludedRegions(),
		},
		true,
	)
	if err != nil {
		return TailLogPage{}, err
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return TailLogPage{}, fmt.Errorf("encode tail positions: %w", err)
	}
	parameters := ch.Parameters{"tail_seen": string(data)}
	for i, arg := range args {
		key := fmt.Sprintf("tail_arg_%d", i)
		placeholder := "{" + key + ":Int64}"
		switch value := arg.(type) {
		case string:
			placeholder = "{" + key + ":String}"
			parameters[key] = value
		case int64:
			parameters[key] = strconv.FormatInt(value, 10)
		case int32:
			parameters[key] = strconv.FormatInt(int64(value), 10)
		default:
			return TailLogPage{}, fmt.Errorf("%w: %T", errTailQueryParameter, arg)
		}
		query = strings.Replace(query, "?", placeholder, 1)
	}
	ctx = ch.Context(ctx, ch.WithParameters(parameters))
	page, err := queryLogPage(ctx, conn, query, nil, limit)
	if err != nil {
		return TailLogPage{}, err
	}
	blocks := make(TailLogCursor)
	for _, position := range page.positions {
		key := position.partitionID + "/" + strconv.FormatUint(position.blockNumber, 10)
		blocks[key] = position.blockOffset
	}
	return TailLogPage{Entries: page.Entries, Blocks: blocks, More: page.NextCursor != ""}, nil
}
