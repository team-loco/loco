package service

import (
	"context"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const tailPageSize = 100

func (s *ObservabilityService) pollTailLogs(
	ctx context.Context,
	request *observabilityv1.TailLogsRequest,
	start, end time.Time,
	cursor *string,
	send func(*observabilityv1.LogEntry) error,
) (int, error) {
	var sent int
	for {
		if err := ctx.Err(); err != nil {
			return sent, err
		}
		page, err := chClient.QueryLogPage(
			ctx,
			s.ch.Conn(),
			request.GetWorkspaceId(),
			request.GetResourceIds(),
			start,
			end,
			request.GetSearch(),
			request.GetLevels(),
			request.GetLabels(),
			tailPageSize,
			*cursor,
			observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
			s.cfg.QueryTimeout,
		)
		if err != nil {
			return sent, err
		}
		for _, entry := range page.Entries {
			if err := send(entry); err != nil {
				return sent, err
			}
			sent++
		}
		if page.LastCursor != "" {
			*cursor = page.LastCursor
		}
		if page.NextCursor == "" {
			return sent, nil
		}
	}
}
