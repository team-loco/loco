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

const (
	defaultTailLookback   = 2 * time.Second
	tailHeartbeatInterval = 5 * time.Second
	tailPollInterval      = 2 * time.Second
)

var _ observabilityv1connect.ObservabilityProxyServiceHandler = (*ObservabilityService)(nil)

type ObservabilityService struct {
	clickhouse *chClient.Client
	cfg        *config.Config
	validator  *auth.Validator
}

func NewObservabilityService(
	clickhouse *chClient.Client,
	cfg *config.Config,
	validator *auth.Validator,
) *ObservabilityService {
	return &ObservabilityService{clickhouse: clickhouse, cfg: cfg, validator: validator}
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

	response, err := s.clickhouse.QueryLogs(ctx, chClient.LogQuery{
		WorkspaceID:     req.GetWorkspaceId(),
		EnvironmentID:   req.GetEnvironmentId(),
		ResourceIDs:     req.GetResourceIds(),
		Start:           req.GetStartTime().AsTime(),
		End:             req.GetEndTime().AsTime(),
		Search:          req.GetSearch(),
		Levels:          req.GetLevels(),
		Labels:          req.GetLabels(),
		Regions:         req.GetRegions(),
		ExcludedRegions: req.GetExcludedRegions(),
		Limit:           limit,
		Cursor:          req.GetCursor(),
		Order:           req.GetOrder(),
	})
	if errors.Is(err, chClient.ErrInvalidLogCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryLogs failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}

	return response, nil
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

func (s *ObservabilityService) tailLoop(
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
			slog.ErrorContext(ctx, "tail poll failed", "error", err)
			return rpcerr.New(connect.CodeInternal, errQueryFailed)
		}

		if sent == 0 && time.Since(lastHeartbeat) >= tailHeartbeatInterval {
			if err := send(&observabilityv1.TailLogsResponse{
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

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
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

	series, err := s.clickhouse.QueryMetrics(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "QueryMetrics failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}

	return &observabilityv1.QueryMetricsResponse{
		Series: series,
	}, nil
}
