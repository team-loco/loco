package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
	observabilityv1 "github.com/team-loco/loco/proto/loco/observability/v1"
	"github.com/team-loco/loco/proto/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/proto/loco/token/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ observabilityv1connect.ObservabilityProxyServiceHandler = (*ObservabilityService)(nil)

type ObservabilityService struct {
	ch        *chClient.Client
	cfg       *config.Config
	validator *auth.Validator
}

func NewObservabilityService(ch *chClient.Client, cfg *config.Config, validator *auth.Validator) *ObservabilityService {
	return &ObservabilityService{ch: ch, cfg: cfg, validator: validator}
}

func (s *ObservabilityService) QueryLogs(
	ctx context.Context,
	req *connect.Request[observabilityv1.QueryLogsRequest],
) (*connect.Response[observabilityv1.QueryLogsResponse], error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token"))
	}

	msg := req.Msg
	if err := guardrails.ValidateLogsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.validator.CheckPermission(ctx, token, tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, msg.GetWorkspaceId(), tokenv1.Scope_SCOPE_READ); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	limit := guardrails.ClampLimit(msg.GetLimit(), s.cfg)

	entries, nextCursor, err := chClient.QueryLogs(
		ctx,
		s.ch.Conn(),
		msg.GetWorkspaceId(),
		msg.GetResourceIds(),
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
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token"))
	}

	msg := req.Msg
	if err := guardrails.ValidateTailRequest(msg, s.cfg); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.validator.CheckPermission(ctx, token, tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, msg.GetWorkspaceId(), tokenv1.Scope_SCOPE_READ); err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	deadline := time.Now().Add(s.cfg.MaxTailDuration)
	lastSeen := time.Now().Add(-2 * time.Second)
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
			msg.GetWorkspaceId(),
			msg.GetResourceIds(),
			lastSeen,
			now,
			msg.GetSearch(),
			msg.GetLevels(),
			msg.GetLabels(),
			100,
			"",
			observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
			s.cfg.QueryTimeout,
		)
		if err != nil {
			slog.ErrorContext(ctx, "tail poll failed", "error", err)
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
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("missing token"))
	}

	msg := req.Msg
	if err := guardrails.ValidateMetricsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.validator.CheckPermission(ctx, token, tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, msg.GetWorkspaceId(), tokenv1.Scope_SCOPE_READ); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	series, err := chClient.QueryMetrics(
		ctx,
		s.ch.Conn(),
		msg.GetWorkspaceId(),
		msg.GetResourceIds(),
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
