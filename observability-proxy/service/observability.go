package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errMissingToken = errors.New("missing token")
	errQueryFailed  = errors.New("query failed")
)

const defaultTailLookback = 2 * time.Second

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
	req *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errMissingToken.Error()).WithCause(errMissingToken)
	}

	msg := req
	if err := guardrails.ValidateLogsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		msg.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return nil, err
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
	if errors.Is(err, chClient.ErrInvalidLogCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryLogs failed", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errQueryFailed.Error()).WithCause(errQueryFailed)
	}

	return &observabilityv1.QueryLogsResponse{
		Entries:    entries,
		NextCursor: nextCursor,
	}, nil
}

func (s *ObservabilityService) TailLogs(
	ctx context.Context,
	req *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
) error {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errMissingToken.Error()).WithCause(errMissingToken)
	}

	msg := req
	started := time.Now()
	if err := guardrails.ValidateTailRequest(msg, s.cfg, started); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		msg.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return err
	}

	deadline := started.Add(s.cfg.MaxTailDuration)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	lastSeen := started.Add(-defaultTailLookback)
	if msg.GetSince() != nil {
		lastSeen = msg.GetSince().AsTime()
	}
	heartbeatInterval := 5 * time.Second
	pollInterval := 2 * time.Second
	lastHeartbeat := time.Now()
	cursor := ""

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
		var sendErr error
		sent, err := s.pollTailLogs(ctx, msg, lastSeen, now, &cursor, func(entry *observabilityv1.LogEntry) error {
			sendErr = stream.Send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Entry{Entry: entry},
			})
			if sendErr != nil {
				return sendErr
			}
			if entry.GetTimestamp().AsTime().After(lastSeen) {
				lastSeen = entry.GetTimestamp().AsTime()
			}
			return nil
		})
		if sendErr != nil {
			return sendErr
		}
		if err != nil {
			slog.ErrorContext(ctx, "tail poll failed", "error", err)
			time.Sleep(pollInterval)
			continue
		}

		if sent == 0 && time.Since(lastHeartbeat) >= heartbeatInterval {
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
	req *observabilityv1.QueryMetricsRequest,
) (*observabilityv1.QueryMetricsResponse, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errMissingToken.Error()).WithCause(errMissingToken)
	}

	msg := req
	if err := guardrails.ValidateMetricsRequest(msg, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		msg.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return nil, err
	}

	series, err := chClient.QueryMetrics(
		ctx,
		s.ch.Conn(),
		msg.GetWorkspaceId(),
		msg.GetEnvironmentId(),
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
		return nil, connect.NewError(connect.CodeInternal, errQueryFailed.Error()).WithCause(errQueryFailed)
	}

	return &observabilityv1.QueryMetricsResponse{
		Series: series,
	}, nil
}
