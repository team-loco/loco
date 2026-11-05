package service

import (
	"context"
	"maps"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const tailPageSize = 100

func (s *ObservabilityService) pollTailLogs(
	ctx context.Context,
	query chClient.LogQuery,
	cursor chClient.TailLogCursor,
	send func(*observabilityv1.LogEntry) error,
) (int, error) {
	var sent int
	for {
		if err := ctx.Err(); err != nil {
			return sent, err
		}
		page, err := s.clickhouse.QueryTailLogPage(ctx, query, cursor)
		if err != nil {
			return sent, err
		}
		for _, entry := range page.Entries {
			if err := send(entry); err != nil {
				return sent, err
			}
			sent++
		}
		maps.Copy(cursor, page.Blocks)
		if !page.More {
			return sent, nil
		}
	}
}
