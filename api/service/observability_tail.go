package service

import (
	"context"
	"fmt"
	"maps"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/pkg/guardrails"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	chClient "github.com/team-loco/loco/internal/clickhouse"
)

const (
	defaultTailLookback   = 2 * time.Second
	tailHeartbeatInterval = 5 * time.Second
	tailPollInterval      = 2 * time.Second
	tailPageSize          = 100
)

func (s *ObservabilityServer) TailLogs(
	ctx context.Context,
	req *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
) error {
	started := time.Now()
	if err := guardrails.ValidateTailRequest(req, s.cfg.Limits, started); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err := s.authorizeRead(ctx, req.GetWorkspaceId(), req.GetResourceIds()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.MaxTailDuration)
	defer cancel()
	since := started.Add(-defaultTailLookback)
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	return s.tailLoop(ctx, tailLogQuery(req, since), stream.Send)
}

func tailLogQuery(req *observabilityv1.TailLogsRequest, since time.Time) chClient.LogQuery {
	return chClient.LogQuery{
		WorkspaceID:     req.GetWorkspaceId(),
		EnvironmentID:   req.GetEnvironmentId(),
		ResourceIDs:     req.GetResourceIds(),
		Start:           since,
		Search:          req.GetSearch(),
		Levels:          req.GetLevels(),
		Labels:          req.GetLabels(),
		Regions:         req.GetRegions(),
		ExcludedRegions: req.GetExcludedRegions(),
		Limit:           tailPageSize,
	}
}

// tailLoop polls until the tail's deadline or the client goes away, both of which end the stream
// cleanly, and sends a heartbeat when a poll finds nothing new for a while.
func (s *ObservabilityServer) tailLoop(
	ctx context.Context,
	query chClient.LogQuery,
	send func(*observabilityv1.TailLogsResponse) error,
) error {
	ticker := time.NewTicker(tailPollInterval)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	cursor := chClient.TailLogCursor{}
	for {
		var sendErr error
		sent, err := s.pollTailLogs(ctx, query, cursor, func(entry *observabilityv1.LogEntry) error {
			sendErr = send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Entry{Entry: entry},
			})
			return sendErr
		})
		if sendErr != nil {
			return sendErr
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("poll tail logs: %w", err)
		}
		if sent == 0 && time.Since(lastHeartbeat) >= tailHeartbeatInterval {
			if err := send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Heartbeat{
					Heartbeat: &observabilityv1.Heartbeat{Timestamp: timestamppb.Now()},
				},
			}); err != nil {
				return err
			}
			lastHeartbeat = time.Now()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// pollTailLogs sends every page of rows the cursor has not seen and advances the cursor only past
// the pages it sent.
func (s *ObservabilityServer) pollTailLogs(
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
