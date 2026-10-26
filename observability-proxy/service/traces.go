package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
)

func (s *ObservabilityService) QueryTraces(
	ctx context.Context,
	req *observabilityv1.QueryTracesRequest,
) (*observabilityv1.QueryTracesResponse, error) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, errMissingToken)
	}
	if err := guardrails.ValidateTracesRequest(req, s.cfg); err != nil {
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
	response, err := s.store.QueryTraces(ctx, req, limit, s.cfg.QueryTimeout)
	if errors.Is(err, chClient.ErrInvalidTraceCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryTraces failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}
	return response, nil
}
