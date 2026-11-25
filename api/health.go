package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	livePath  = "/livez"
	readyPath = "/readyz"

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

// readyHandler answers 200 when every check passes within readinessTimeout, and otherwise
// 503 naming the first failing dependency without its error text.
func readyHandler(checks ...readinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
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
