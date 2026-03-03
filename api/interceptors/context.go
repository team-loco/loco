package interceptors

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
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

func (i *contextInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		slog.Info("adding additional request context",
			slog.String("user-agent", req.Header().Get("User-Agent")),
			slog.String("content-type", req.Header().Get("Content-Type")),
		)

		rid := resolveRequestID(req.Header().Get("X-Loco-Request-Id"))

		ctx = context.WithValue(ctx, contextkeys.RequestIDKey, rid)
		ctx = context.WithValue(ctx, contextkeys.PathKey, req.Spec().Procedure)

		start := time.Now()
		resp, err := next(ctx, req)
		dur := time.Since(start)
		durMilli := float64(dur.Milliseconds())

		slog.InfoContext(
			ctx,
			"handled request",
			slog.String("duration", dur.String()),
		)

		if err == nil && resp != nil {
			resp.Header().Set("X-Loco-Request-Id", rid)
			resp.Header().Set("Server-Timing", fmt.Sprintf(`rid;desc="%s";dur=%.2f`, rid, durMilli))
		}

		return resp, err
	})
}

func (i *contextInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return connect.StreamingClientFunc(func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	})
}

func (i *contextInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		slog.Info("adding additional request context",
			slog.String("user-agent", conn.RequestHeader().Get("User-Agent")),
			slog.String("content-type", conn.RequestHeader().Get("Content-Type")),
		)

		rid := resolveRequestID(conn.RequestHeader().Get("X-Loco-Request-Id"))

		ctx = context.WithValue(ctx, contextkeys.RequestIDKey, rid)
		ctx = context.WithValue(ctx, contextkeys.PathKey, conn.Spec().Procedure)

		conn.ResponseHeader().Set("X-Loco-Request-Id", rid)

		start := time.Now()
		err := next(ctx, conn)
		duration := time.Since(start)
		durMilli := float64(duration.Microseconds())

		slog.InfoContext(
			ctx,
			"handled request",
			slog.String("duration", duration.String()),
		)

		conn.ResponseTrailer().Set("Server-Timing", fmt.Sprintf(`rid;desc="%s";dur=%.2f`, rid, durMilli))

		return err
	})
}
