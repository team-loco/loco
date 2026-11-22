package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName     = "github.com/team-loco/loco/api"
	traceparentKey = "traceparent"
)

// Start names one outbound operation, whose client calls become its children.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, trace.WithAttributes(attrs...)) //nolint:spancheck
}

func StartRoot(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	opts = append(opts, trace.WithNewRoot())
	return otel.Tracer(tracerName).Start(ctx, name, opts...) //nolint:spancheck
}

// StartLinked starts a root span linked to the span in ctx.
func StartLinked(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	opts = append(opts, trace.WithLinks(trace.LinkFromContext(ctx)))
	return StartRoot(ctx, name, opts...)
}

// Link links span to the span that started its work, given that span's stored traceparent.
func Link(span trace.Span, traceparent *string) {
	if traceparent == nil {
		return
	}
	if link, ok := LinkTo(*traceparent); ok {
		span.AddLink(link)
	}
}

func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// EndLive ends span and records err only while ctx is live, leaving a stream's clean end without an error.
func EndLive(ctx context.Context, span trace.Span, err error) {
	if ctx.Err() != nil {
		err = nil
	}
	End(span, err)
}

func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier[traceparentKey]
}

// Origin is the traceparent to store with work that outlives the request, or nil outside a trace.
func Origin(ctx context.Context) *string {
	if traceparent := Traceparent(ctx); traceparent != "" {
		return &traceparent
	}
	return nil
}

func LinkTo(traceparent string) (trace.Link, bool) {
	carrier := propagation.MapCarrier{traceparentKey: traceparent}
	ctx := propagation.TraceContext{}.Extract(context.Background(), carrier)
	spanContext := trace.SpanContextFromContext(ctx)
	return trace.Link{SpanContext: spanContext}, spanContext.IsValid()
}
