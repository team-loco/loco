package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName     = "github.com/team-loco/loco/api"
	traceparentKey = "traceparent"
)

func StartRoot(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	opts = append(opts, trace.WithNewRoot())
	return otel.Tracer(tracerName).Start(ctx, name, opts...) //nolint:spancheck
}

func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier[traceparentKey]
}

func LinkTo(traceparent string) (trace.Link, bool) {
	carrier := propagation.MapCarrier{traceparentKey: traceparent}
	ctx := propagation.TraceContext{}.Extract(context.Background(), carrier)
	spanContext := trace.SpanContextFromContext(ctx)
	return trace.Link{SpanContext: spanContext}, spanContext.IsValid()
}
