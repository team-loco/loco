package main

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/contextkeys"
)

type CustomHandler struct {
	slog.Handler
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	requestID, ok := ctx.Value(contextkeys.RequestIDKey).(string)
	if !ok {
		return l.Handler.Handle(ctx, r)
	}
	path, okPath := ctx.Value(contextkeys.PathKey).(string)
	if !okPath {
		path = ""
	}

	// can be null on routes where oAuth Middleware is skipped.
	entity := ctx.Value(contextkeys.EntityKey)

	requestGroup := slog.Group(
		"request",
		slog.String("requestId", requestID),
		slog.String("path", path),
		slog.Any("entity", entity),
	)

	r.AddAttrs(requestGroup)

	return l.Handler.Handle(ctx, r)
}
