package interceptor

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/internal/rpc"
)

type contextInterceptor struct{}

func NewContextInterceptor() *contextInterceptor {
	return &contextInterceptor{}
}

func (*contextInterceptor) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			slog.DebugContext(ctx, "adding additional request context",
				slog.String("user-agent", info.RequestHeader().Get("User-Agent")),
				slog.String("content-type", info.RequestHeader().Get("Content-Type")))
			rid := rpc.ResolveRequestID(info.RequestHeader().Get(rpc.RequestIDHeader))
			ctx = reqctx.WithRequest(ctx, reqctx.Request{ID: rid, Procedure: spec.Procedure})
			info.ResponseHeader().Set(rpc.RequestIDHeader, rid)
			start := time.Now()
			err := next(ctx, spec, stream)
			duration := time.Since(start)
			if err != nil {
				slog.DebugContext(
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
