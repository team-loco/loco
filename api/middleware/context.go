package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
)

func SetContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("adding additional request context")

		requestHeader := w.Header().Get("X-Loco-Request-Id")

		// only generate a new request header if one already doesn't exist
		if requestHeader == "" {
			requestHeader = uuid.NewString()
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, contextkeys.RequestIDKey, requestHeader)
		ctx = context.WithValue(ctx, contextkeys.MethodKey, r.Method)
		ctx = context.WithValue(ctx, contextkeys.PathKey, r.URL.Path)
		ctx = context.WithValue(ctx, contextkeys.SourceIPKey, r.RemoteAddr)

		w.Header().Set("X-Loco-Request-Id", requestHeader)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
