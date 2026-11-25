package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/transit"
)

const (
	livePath  = "/livez"
	readyPath = "/readyz"
	// healthPath is the liveness path the chart of the latest release probes.
	healthPath = "/health"

	readinessTimeout = 2 * time.Second
)

// readinessCheck probes one dependency the API needs on every request.
type readinessCheck struct {
	name  string
	probe func(context.Context) error
}

// liveHandler answers 200 while the process serves HTTP and checks no dependency.
func liveHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, "ok")
}

// newReadyHandler checks the database and the OpenBao transit ingest key.
func newReadyHandler(pool *pgxpool.Pool, ingest *transit.Signer) http.HandlerFunc {
	return readyHandler(
		readinessCheck{"postgres", pool.Ping},
		readinessCheck{"transit", func(ctx context.Context) error {
			_, err := ingest.KeyID(ctx)
			return err
		}},
	)
}

// unsampledProbe is a parent that makes every span of a probe's dependency calls non-recording.
var unsampledProbe = trace.NewSpanContext(trace.SpanContextConfig{
	TraceID: trace.TraceID{1},
	SpanID:  trace.SpanID{1},
	Remote:  true,
})

// readyHandler answers 200 when every check passes within readinessTimeout, and otherwise
// 503 naming the first failing dependency without its error text.
func readyHandler(checks ...readinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(
			trace.ContextWithRemoteSpanContext(r.Context(), unsampledProbe), readinessTimeout,
		)
		defer cancel()
		for _, check := range checks {
			if err := check.probe(ctx); err != nil {
				slog.WarnContext(ctx, "readiness check failed", "check", check.name, "error", err)
				http.Error(w, check.name+" unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		fmt.Fprintln(w, "ok")
	}
}
