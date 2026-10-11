package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	listenAddr               = ":8080"
	readHeaderTimeout        = 5 * time.Second
	shutdownTimeout          = 10 * time.Second
	tracerName               = "github.com/team-loco/loco/e2e/fixtures/otel-app"
	forgedSpanName           = "forged-workspace-span"
	forgedWorkspaceAttribute = "loco.workspace.id"
	forgedWorkspaceID        = "forged-workspace"
)

type app struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	logger     *slog.Logger
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("otel-app stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return fmt.Errorf("build resource: %w", err)
	}
	provider, err := newProvider(ctx, res)
	if err != nil {
		return err
	}
	forgedAttribute := attribute.String(forgedWorkspaceAttribute, forgedWorkspaceID)
	forgedOverride := resource.NewSchemaless(forgedAttribute)
	forgedRes, err := resource.Merge(res, forgedOverride)
	if err != nil {
		return fmt.Errorf("build forged resource: %w", err)
	}
	alwaysSample := sdktrace.WithSampler(sdktrace.AlwaysSample())
	forgedProvider, err := newProvider(ctx, forgedRes, alwaysSample)
	if err != nil {
		return err
	}
	defer shutdown(logger, provider, forgedProvider)

	forgedTracer := forgedProvider.Tracer(tracerName)
	_, forgedSpan := forgedTracer.Start(ctx, forgedSpanName)
	forgedSpan.End()

	a := &app{
		tracer:     provider.Tracer(tracerName),
		propagator: propagation.TraceContext{},
		logger:     logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /", a.serve)
	server := &http.Server{Addr: listenAddr, Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ListenAndServe()
	}()
	logger.Info("otel-app listening", "addr", listenAddr)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shut down server: %w", err)
	}
	return nil
}

func newProvider(
	ctx context.Context,
	res *resource.Resource,
	extra ...sdktrace.TracerProviderOption,
) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp exporter: %w", err)
	}
	batcher := sdktrace.WithBatcher(exporter)
	withResource := sdktrace.WithResource(res)
	options := append([]sdktrace.TracerProviderOption{batcher, withResource}, extra...)
	return sdktrace.NewTracerProvider(options...), nil
}

func shutdown(logger *slog.Logger, providers ...*sdktrace.TracerProvider) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, provider := range providers {
		if err := provider.Shutdown(ctx); err != nil {
			logger.Error("shut down tracer provider", "error", err)
		}
	}
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (a *app) serve(w http.ResponseWriter, r *http.Request) {
	carrier := propagation.HeaderCarrier(r.Header)
	parent := a.propagator.Extract(r.Context(), carrier)
	spanName := r.Method + " " + r.URL.Path
	kind := trace.WithSpanKind(trace.SpanKindServer)
	method := attribute.String("http.request.method", r.Method)
	path := attribute.String("url.path", r.URL.Path)
	attributes := trace.WithAttributes(method, path)
	_, span := a.tracer.Start(parent, spanName, kind, attributes)
	defer span.End()

	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintln(w, "ok"); err != nil {
		a.logger.Error("write response", "error", err)
	}
	spanContext := span.SpanContext()
	a.logger.Info(
		"request",
		"trace_id", spanContext.TraceID().String(),
		"span_id", spanContext.SpanID().String(),
		"method", r.Method,
		"path", r.URL.Path,
		"status", http.StatusOK,
	)
}
