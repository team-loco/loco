package auth

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

type streamAbortKey struct{}

// StreamTransport lets credential revocation interrupt an HTTP stream whose client
// has stopped reading. HTTP/2 deadlines apply to the stream; expired HTTP/1
// writes fail the connection. A short grace lets responsive clients receive the
// RPC error, after which clearing the deadline permits connection reuse.
func StreamTransport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var aborted atomic.Bool
		controller := http.NewResponseController(w)
		abort := func(grace time.Duration) {
			aborted.Store(true)
			if err := controller.SetWriteDeadline(time.Now().Add(grace)); err != nil {
				slog.ErrorContext(r.Context(), "failed to bound revoked stream write", "error", err)
			}
		}
		defer func() {
			if aborted.Load() {
				if err := controller.SetWriteDeadline(time.Time{}); err != nil {
					slog.ErrorContext(r.Context(), "failed to clear stream write deadline", "error", err)
				}
			}
		}()
		ctx := context.WithValue(r.Context(), streamAbortKey{}, abort)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
