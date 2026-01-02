package main

import (
	"context"
	"log/slog"

	"github.com/team-loco/loco/api/contextkeys"
)

type CustomHandler struct {
	slog.Handler
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx.Value(contextkeys.RequestIDKey) == nil {
		return l.Handler.Handle(ctx, r)
	}

	requestId := ctx.Value(contextkeys.RequestIDKey).(string)
	sourceIp := ctx.Value(contextkeys.SourceIPKey).(string)
	path := ctx.Value(contextkeys.PathKey).(string)
	method := ctx.Value(contextkeys.MethodKey).(string)

	// can be null on routes where oAuth Middleware is skipped.
	entity := ctx.Value(contextkeys.EntityKey)

	requestGroup := slog.Group(
		"request",
		slog.String("requestId", requestId),
		slog.String("sourceIp", sourceIp),
		slog.String("method", method),
		slog.String("path", path),
		slog.Any("entity", entity),
	)

	r.AddAttrs(requestGroup)

	return l.Handler.Handle(ctx, r)
}
