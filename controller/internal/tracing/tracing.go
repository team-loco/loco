// Package tracing records reconciles as spans and the Kubernetes API calls they make as client spans.
package tracing

import (
	"context"
	"errors"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	tracerName       = "github.com/team-loco/loco/controller"
	objectNameAttr   = "loco.object.name"
	requeueAfterAttr = "loco.reconcile.requeue_after"
	waitingAttr      = "loco.reconcile.waiting"
	traceparentKey   = "traceparent"
)

func start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, trace.WithAttributes(attrs...)) //nolint:spancheck
}

func end(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// Reconcile runs fn in a span named name for the object req names, recording its result.
func Reconcile(
	ctx context.Context,
	name string,
	req ctrl.Request,
	fn func(context.Context) (ctrl.Result, error),
) (ctrl.Result, error) {
	ctx, span := start(ctx, name,
		semconv.K8SNamespaceName(req.Namespace), attribute.String(objectNameAttr, req.Name))
	result, err := fn(ctx)
	if result.RequeueAfter > 0 {
		span.SetAttributes(attribute.String(requeueAfterAttr, result.RequeueAfter.String()))
	}
	end(span, err)
	return result, err
}

// Link links the span in ctx to the span a W3C traceparent names, ignoring an empty or invalid one.
func Link(ctx context.Context, traceparent string) {
	carrier := propagation.MapCarrier{traceparentKey: traceparent}
	origin := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), carrier))
	if origin.IsValid() {
		trace.SpanFromContext(ctx).AddLink(trace.Link{SpanContext: origin})
	}
}

// Step runs fn in a child span named name.
func Step(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := start(ctx, name)
	err := fn(ctx)
	end(span, err)
	return err
}

// Wait runs fn in a child span named name, marking the span waiting instead of failed when fn returns pending.
func Wait(ctx context.Context, name string, pending error, fn func(context.Context) error) error {
	ctx, span := start(ctx, name)
	err := fn(ctx)
	if errors.Is(err, pending) {
		span.SetAttributes(attribute.Bool(waitingAttr, true))
		span.End()
		return err
	}
	end(span, err)
	return err
}

// TraceRequests records every request a client built from cfg makes as a client span.
func TraceRequests(cfg *rest.Config) {
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(rt)
	})
}
