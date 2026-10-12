package interceptor

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
)

type contextInterceptor struct{}

func NewContextInterceptor() *contextInterceptor {
	return &contextInterceptor{}
}

func resolveRequestID(existing string) string {
	if existing != "" {
		return existing
	}
	id, err := uuid.NewV7()
	if err != nil {
		slog.Warn("failed to generate request ID, using empty string", "error", err)
		return ""
	}
	return id.String()
}

func (*contextInterceptor) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			slog.DebugContext(ctx, "adding additional request context",
				slog.String("user-agent", info.RequestHeader().Get("User-Agent")),
				slog.String("content-type", info.RequestHeader().Get("Content-Type")))
			rid := resolveRequestID(info.RequestHeader().Get("X-Loco-Request-Id"))
			ctx = context.WithValue(ctx, contextkeys.RequestIDKey, rid)
			ctx = context.WithValue(ctx, contextkeys.PathKey, spec.Procedure)
			info.ResponseHeader().Set("X-Loco-Request-Id", rid)
			start := time.Now()
			err := next(ctx, spec, stream)
			duration := time.Since(start)
			if err != nil {
				slog.WarnContext(
					ctx,
					"request failed",
					"duration",
					duration.String(),
					"code",
					connect.CodeOf(err).String(),
					"error",
					err.Error(),
				)
			} else {
				slog.InfoContext(ctx, "handled request", "duration", duration.String())
			}
			return err
		}
	}
}
