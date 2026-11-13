package service

import (
	"context"
	"log/slog"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpc"
)

// LogHandler adds the request ID and procedure to records logged during an RPC, under the same
// keys the API logs them, so one ID finds a request in both services' logs.
type LogHandler struct {
	slog.Handler
}

func (h LogHandler) Handle(ctx context.Context, record slog.Record) error {
	if id, ok := rpc.RequestIDFrom(ctx); ok {
		info, _ := connect.CallInfoForServerContext(ctx)
		record.AddAttrs(slog.Group("request",
			slog.String("requestId", id),
			slog.String("path", info.Spec.Procedure),
		))
	}
	return h.Handler.Handle(ctx, record)
}

func (h LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return LogHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h LogHandler) WithGroup(name string) slog.Handler {
	return LogHandler{Handler: h.Handler.WithGroup(name)}
}

// requestIDInterceptor keeps the request ID the API forwards, or mints one for a direct caller,
// and puts it on the context so log lines and calls back to the API carry it.
func requestIDInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			rid := rpc.ResolveRequestID(info.RequestHeader().Get(rpc.RequestIDHeader))
			info.ResponseHeader().Set(rpc.RequestIDHeader, rid)
			ctx = rpc.WithRequestID(ctx, rid)
			return next(ctx, spec, stream)
		}
	}
}
