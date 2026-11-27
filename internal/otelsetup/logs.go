package otelsetup

import (
	"context"
	"log/slog"
	"slices"

	"go.opentelemetry.io/otel/trace"
)

// LogHandler preserves trace correlation and service context in structured stdout logs.
// Context fields stay at the top level even when callers derive grouped loggers.
func LogHandler(output slog.Handler, attrs func(context.Context) []slog.Attr) slog.Handler {
	return contextHandler{Handler: output, attrs: attrs}
}

type contextHandler struct {
	slog.Handler
	derive func(slog.Handler) slog.Handler
	attrs  func(context.Context) []slog.Attr
}

func (l contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: l.Handler, attrs: l.attrs, derive: func(h slog.Handler) slog.Handler {
		if l.derive != nil {
			h = l.derive(h)
		}
		return h.WithAttrs(slices.Clone(attrs))
	}}
}

func (l contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: l.Handler, attrs: l.attrs, derive: func(h slog.Handler) slog.Handler {
		if l.derive != nil {
			h = l.derive(h)
		}
		return h.WithGroup(name)
	}}
}

func (l contextHandler) Handle(ctx context.Context, r slog.Record) error {
	var attrs []slog.Attr
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		attrs = append(attrs,
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}

	if l.attrs != nil {
		attrs = append(attrs, l.attrs(ctx)...)
	}

	// Correlation fields must precede caller groups so log queries can find them.
	handler := l.Handler.WithAttrs(attrs)
	if l.derive != nil {
		handler = l.derive(handler)
	}
	return handler.Handle(ctx, r)
}
