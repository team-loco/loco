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

type Store interface {
	QueryLogs(
		ctx context.Context, workspaceID string, resourceIDs []string,
		startTime, endTime time.Time, search string, levels []string, labels map[string]string,
		limit int32, cursor string, order observabilityv1.LogOrder, queryTimeout int, scope chClient.LogScope,
	) ([]*observabilityv1.LogEntry, string, error)
	QueryTailLogPage(
		ctx context.Context, request *observabilityv1.TailLogsRequest, since time.Time,
		limit int32, queryTimeout int, cursor chClient.TailLogCursor,
	) (chClient.TailLogPage, error)
	QueryMetrics(
		ctx context.Context, workspaceID, environmentID, region string, resourceIDs []string,
		startTime, endTime time.Time, metricName string, intervalSeconds int32, aggregation string,
		queryTimeout int,
	) ([]*observabilityv1.MetricSeries, error)
	QueryTraces(
		ctx context.Context, req *observabilityv1.QueryTracesRequest, limit int32, queryTimeout int,
	) (*observabilityv1.QueryTracesResponse, error)
}

type ObservabilityService struct {
	store     Store
	cfg       *config.Config
	validator *auth.Validator
}

func NewObservabilityService(store Store, cfg *config.Config, validator *auth.Validator) *ObservabilityService {
	return &ObservabilityService{store: store, cfg: cfg, validator: validator}
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

	entries, nextCursor, err := s.store.QueryLogs(
		ctx,
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

	ctx, cancel := context.WithTimeout(ctx, s.cfg.MaxTailDuration)
	defer cancel()
	since := started.Add(-defaultTailLookback)
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	return s.tailLoop(ctx, req, since, stream.Send)
}

func (s *ObservabilityService) tailLoop(
	ctx context.Context,
	req *observabilityv1.TailLogsRequest,
	since time.Time,
	send func(*observabilityv1.TailLogsResponse) error,
) error {
	ticker := time.NewTicker(tailPollInterval)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	cursor := chClient.TailLogCursor{}

	for {
		var sendErr error
		sent, err := s.pollTailLogs(ctx, req, since, cursor, func(entry *observabilityv1.LogEntry) error {
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

	series, err := s.store.QueryMetrics(
		ctx,
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
