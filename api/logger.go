package main

import (
	"context"
	"log/slog"

	"github.com/team-loco/loco/api/reqctx"
)

func requestLogAttrs(ctx context.Context) []slog.Attr {
	request, ok := reqctx.RequestFrom(ctx)
	if !ok {
		return nil
	}
	// Public procedures run without a principal; their entity logs as null.
	var entity any
	if principal, ok := reqctx.PrincipalFrom(ctx); ok {
		entity = principal.Entity
	}
	return []slog.Attr{slog.Group("request",
		slog.String("requestId", request.ID),
		slog.String("path", request.Procedure),
		slog.Any("entity", entity),
	)}
}
