package main

import (
	"context"
	"log/slog"
	"slices"

	"github.com/team-loco/loco/api/reqctx"
	"go.opentelemetry.io/otel/trace"
)

type CustomHandler struct {
	slog.Handler
	derive func(slog.Handler) slog.Handler
}

func (l CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return CustomHandler{Handler: l.Handler, derive: func(h slog.Handler) slog.Handler {
		if l.derive != nil {
			h = l.derive(h)
		}
		return h.WithAttrs(slices.Clone(attrs))
	}}
}

func (l CustomHandler) WithGroup(name string) slog.Handler {
	return CustomHandler{Handler: l.Handler, derive: func(h slog.Handler) slog.Handler {
		if l.derive != nil {
			h = l.derive(h)
		}
		return h.WithGroup(name)
	}}
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	var attrs []slog.Attr
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		attrs = append(attrs,
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	request, ok := reqctx.RequestFrom(ctx)
	if ok {
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

		attrs = append(attrs, requestGroup)
	}

	// Correlation fields must precede caller groups so log queries can find them.
	handler := l.Handler.WithAttrs(attrs)
	if l.derive != nil {
		handler = l.derive(handler)
	}
	return handler.Handle(ctx, r)
}
