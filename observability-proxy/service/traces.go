package service

import (
	"context"
	"log/slog"

	"connectrpc.com/connect/v2"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/observability-proxy/pkg/guardrails"
)

func (s *ObservabilityService) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
) (*observabilityv1.ListTracesResponse, error) {
	if err := guardrails.ValidateListTracesRequest(req, s.cfg); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	if err := s.authorizeTraceRead(ctx, req.GetScope()); err != nil {
		return nil, err
	}
	response, err := s.clickhouse.ListTraces(ctx, req, guardrails.ClampLimit(req.GetLimit(), s.cfg))
	if err != nil {
		slog.ErrorContext(ctx, "ListTraces failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}
	return response, nil
}

func (s *ObservabilityService) GetTrace(
	ctx context.Context,
	req *observabilityv1.GetTraceRequest,
) (*observabilityv1.GetTraceResponse, error) {
	if err := s.authorizeTraceRead(ctx, req.GetScope()); err != nil {
		return nil, err
	}
	response, err := s.clickhouse.GetTrace(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "GetTrace failed", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, errQueryFailed)
	}
	return response, nil
}

func (s *ObservabilityService) authorizeTraceRead(ctx context.Context, scope *observabilityv1.TraceScope) error {
	if err := guardrails.ValidateTraceScope(scope); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
	}
	return s.authorizeRead(ctx, scope.GetWorkspaceId(), scope.GetResourceIds())
}
