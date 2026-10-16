package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
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
		return nil, rpcerr.New(connect.CodeUnauthenticated, errMissingToken)
	}

	if err := guardrails.ValidateLogsRequest(req, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		req.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return nil, err
	}

	limit := guardrails.ClampLimit(req.GetLimit(), s.cfg)

	entries, nextCursor, err := chClient.QueryLogs(
		ctx,
		s.ch.Conn(),
		req.GetWorkspaceId(),
		req.GetResourceIds(),
		req.GetStartTime().AsTime(),
		req.GetEndTime().AsTime(),
		req.GetSearch(),
		req.GetLevels(),
		req.GetLabels(),
		limit,
		req.GetCursor(),
		req.GetOrder(),
		s.cfg.QueryTimeout,
		chClient.LogScope{
			EnvironmentID: req.GetEnvironmentId(),
			Include:       req.GetRegions(),
			Exclude:       req.GetExcludedRegions(),
		},
	)
	if errors.Is(err, chClient.ErrInvalidLogCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryLogs failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
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
		return rpcerr.New(connect.CodeUnauthenticated, errMissingToken)
	}

	started := time.Now()
	if err := guardrails.ValidateTailRequest(req, s.cfg, started); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		req.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return err
	}

	deadline := started.Add(s.cfg.MaxTailDuration)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	since := started.Add(-defaultTailLookback)
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	heartbeatInterval := 5 * time.Second
	pollInterval := 2 * time.Second
	lastHeartbeat := time.Now()
	cursor := chClient.TailLogCursor{}

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
		var sendErr error
		sent, err := s.pollTailLogs(ctx, req, since, cursor, func(entry *observabilityv1.LogEntry) error {
			sendErr = stream.Send(&observabilityv1.TailLogsResponse{
				Event: &observabilityv1.TailLogsResponse_Entry{Entry: entry},
			})
			if sendErr != nil {
				return sendErr
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
		return nil, rpcerr.New(connect.CodeUnauthenticated, errMissingToken)
	}

	if err := guardrails.ValidateMetricsRequest(req, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}

	if err := s.validator.CheckPermission(
		ctx,
		token,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		req.GetWorkspaceId(),
		tokenv1.Scope_SCOPE_READ,
	); err != nil {
		return nil, err
	}

	series, err := chClient.QueryMetrics(
		ctx,
		s.ch.Conn(),
		req.GetWorkspaceId(),
		req.GetEnvironmentId(),
		req.GetRegion(),
		req.GetResourceIds(),
		req.GetStartTime().AsTime(),
		req.GetEndTime().AsTime(),
		req.GetMetricName(),
		req.GetIntervalSeconds(),
		req.GetAggregation(),
		s.cfg.QueryTimeout,
	)
	if err != nil {
		slog.ErrorContext(ctx, "QueryMetrics failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}

	return &observabilityv1.QueryMetricsResponse{
		Series: series,
	}, nil
}
