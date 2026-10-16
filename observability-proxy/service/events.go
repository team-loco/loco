package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
)

func (s *ObservabilityService) QueryEvents(
	ctx context.Context,
	req *observabilityv1.QueryEventsRequest,
) (*observabilityv1.QueryEventsResponse, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, errMissingToken)
	}
	if err := guardrails.ValidateEventsRequest(req, s.cfg); err != nil {
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
	response, err := chClient.QueryEvents(
		ctx,
		s.ch.Conn(),
		req,
		chClient.EventQueryOptions{
			Limit:     guardrails.ClampLimit(req.GetLimit(), s.cfg),
			Timeout:   s.cfg.QueryTimeout,
			Retention: s.cfg.EventsTTL,
			CursorTTL: s.cfg.MaxTimeRange,
			Now:       time.Now(),
		},
	)
	if errors.Is(err, chClient.ErrInvalidEventCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryEvents failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}
	return response, nil
}
