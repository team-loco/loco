package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
)

func (s *ObservabilityService) QueryTraces(
	ctx context.Context,
	req *observabilityv1.QueryTracesRequest,
) (*observabilityv1.QueryTracesResponse, error) {
	if err := guardrails.ValidateTracesRequest(req, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err := s.authorizeRead(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	limit := guardrails.ClampLimit(req.GetLimit(), s.cfg)
	response, err := s.clickhouse.QueryTraces(ctx, req, limit)
	if errors.Is(err, chClient.ErrInvalidTraceCursor) {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err != nil {
		slog.ErrorContext(ctx, "QueryTraces failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}
	return response, nil
}
