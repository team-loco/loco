package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
	observabilityv1 "github.com/team-loco/loco/proto/loco/observability/v1"
	"github.com/team-loco/loco/proto/loco/observability/v1/observabilityv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ observabilityv1connect.ObservabilityProxyServiceHandler = (*ObservabilityService)(nil)

type ObservabilityService struct {
	ch  *chClient.Client
	cfg *config.Config
}

func NewObservabilityService(ch *chClient.Client, cfg *config.Config) *ObservabilityService {
	return &ObservabilityService{ch: ch, cfg: cfg}
}

func (s *ObservabilityService) QueryLogs(
	ctx context.Context,
	req *connect.Request[observabilityv1.QueryLogsRequest],
) (*connect.Response[observabilityv1.QueryLogsResponse], error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing claims"))
	}

	msg := req.Msg
	if err := enforceScope(claims, msg.GetWorkspaceId(), msg.GetResourceIds()); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if err := guardrails.ValidateLogsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	limit := guardrails.ClampLimit(msg.GetLimit(), s.cfg)
	resourceIDs := effectiveResourceIDs(claims, msg.GetResourceIds())

	entries, nextCursor, err := chClient.QueryLogs(
		ctx,
		s.ch.Conn(),
		claims.WorkspaceID,
		resourceIDs,
		msg.GetStartTime().AsTime(),
		msg.GetEndTime().AsTime(),
		msg.GetSearch(),
		msg.GetLevels(),
		msg.GetLabels(),
		limit,
		msg.GetCursor(),
		msg.GetOrder(),
		s.cfg.QueryTimeout,
	)
	if err != nil {
		slog.ErrorContext(ctx, "QueryLogs failed", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query failed"))
	}

	return connect.NewResponse(&observabilityv1.QueryLogsResponse{
		Entries:    entries,
		NextCursor: nextCursor,
	}), nil
}

func (s *ObservabilityService) TailLogs(
	ctx context.Context,
	req *connect.Request[observabilityv1.TailLogsRequest],
	stream *connect.ServerStream[observabilityv1.TailLogsResponse],
) error {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing claims"))
	}

	msg := req.Msg
	if err := enforceScope(claims, msg.GetWorkspaceId(), msg.GetResourceIds()); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	if err := guardrails.ValidateTailRequest(msg, s.cfg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	resourceIDs := effectiveResourceIDs(claims, msg.GetResourceIds())
	deadline := time.Now().Add(s.cfg.MaxTailDuration)
	lastSeen := time.Now().Add(-2 * time.Second) // start from 2s ago
	heartbeatInterval := 5 * time.Second
	pollInterval := 2 * time.Second
	lastHeartbeat := time.Now()

	for {
		if time.Now().After(deadline) {
			slog.InfoContext(ctx, "tail duration exceeded, closing stream")
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		default:
		}

		now := time.Now()
		entries, _, err := chClient.QueryLogs(
			ctx,
			s.ch.Conn(),
			claims.WorkspaceID,
			resourceIDs,
			lastSeen,
			now,
			msg.GetSearch(),
			msg.GetLevels(),
			msg.GetLabels(),
			100, // small batch for tail
			"",
			observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
			s.cfg.QueryTimeout,
		)
		if err != nil {
			slog.ErrorContext(ctx, "tail poll failed", "error", err)
			// Don't kill the stream on transient errors, just skip this poll
			time.Sleep(pollInterval)
			continue
		}

		for _, entry := range entries {
			if err := stream.Send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Entry{Entry: entry},
			}); err != nil {
				return err
			}
			if entry.GetTimestamp().AsTime().After(lastSeen) {
				lastSeen = entry.GetTimestamp().AsTime()
			}
		}

		// Send heartbeat if no entries and interval elapsed
		if len(entries) == 0 && time.Since(lastHeartbeat) >= heartbeatInterval {
			if err := stream.Send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Heartbeat{
					Heartbeat: &observabilityv1.Heartbeat{
						Timestamp: timestamppb.Now(),
					},
				},
			}); err != nil {
				return err
			}
			lastHeartbeat = time.Now()
		}

		time.Sleep(pollInterval)
	}
}

func (s *ObservabilityService) QueryMetrics(
	ctx context.Context,
	req *connect.Request[observabilityv1.QueryMetricsRequest],
) (*connect.Response[observabilityv1.QueryMetricsResponse], error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing claims"))
	}

	msg := req.Msg
	if err := enforceScope(claims, msg.GetWorkspaceId(), msg.GetResourceIds()); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if err := guardrails.ValidateMetricsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	resourceIDs := effectiveResourceIDs(claims, msg.GetResourceIds())

	series, err := chClient.QueryMetrics(
		ctx,
		s.ch.Conn(),
		claims.WorkspaceID,
		resourceIDs,
		msg.GetStartTime().AsTime(),
		msg.GetEndTime().AsTime(),
		msg.GetMetricName(),
		msg.GetIntervalSeconds(),
		msg.GetAggregation(),
		s.cfg.QueryTimeout,
	)
	if err != nil {
		slog.ErrorContext(ctx, "QueryMetrics failed", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query failed"))
	}

	return connect.NewResponse(&observabilityv1.QueryMetricsResponse{
		Series: series,
	}), nil
}

// enforceScope ensures the request's workspace and resources match the token claims.
func enforceScope(claims *auth.ValidatedClaims, requestedWorkspace string, requestedResources []string) error {
	if requestedWorkspace != claims.WorkspaceID {
		return fmt.Errorf("workspace mismatch: token is scoped to %s", claims.WorkspaceID)
	}

	// If claims restrict to specific resources, validate the request doesn't exceed them
	if len(claims.ResourceIDs) > 0 && len(requestedResources) > 0 {
		for _, rid := range requestedResources {
			if !slices.Contains(claims.ResourceIDs, rid) {
				return fmt.Errorf("resource %s not in token scope", rid)
			}
		}
	}

	return nil
}

// effectiveResourceIDs returns the resource IDs to filter by: the request's if specified,
// otherwise falls back to the token's scope.
func effectiveResourceIDs(claims *auth.ValidatedClaims, requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	return claims.ResourceIDs
}
