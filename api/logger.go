package main

import (
	"context"
	"log/slog"

	"github.com/team-loco/loco/api/reqctx"
	"go.opentelemetry.io/otel/trace"
)

type CustomHandler struct {
	slog.Handler
}

func (l CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return CustomHandler{Handler: l.Handler.WithAttrs(attrs)}
}

func (l CustomHandler) WithGroup(name string) slog.Handler {
	return CustomHandler{Handler: l.Handler.WithGroup(name)}
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	request, ok := reqctx.RequestFrom(ctx)
	if !ok {
		return l.Handler.Handle(ctx, r)
	}
	// Public procedures run without a principal; their entity logs as null.
	var entity any
	if principal, ok := reqctx.PrincipalFrom(ctx); ok {
		entity = principal.Entity
	}

	requestGroup := slog.Group(
		"request",
		slog.String("requestId", request.ID),
		slog.String("path", request.Procedure),
		slog.Any("entity", entity),
	)

	r.AddAttrs(requestGroup)

	return l.Handler.Handle(ctx, r)
}
