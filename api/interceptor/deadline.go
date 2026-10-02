package interceptor

import (
	"context"
	"time"

	"connectrpc.com/connect"
)

type deadlineInterceptor struct {
	shutdownCtx  context.Context
	unaryTimeout time.Duration
}

func NewDeadlineInterceptor(shutdownCtx context.Context, unaryTimeout time.Duration) *deadlineInterceptor {
	return &deadlineInterceptor{
		shutdownCtx:  shutdownCtx,
		unaryTimeout: unaryTimeout,
	}
}

func (i *deadlineInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return connect.UnaryFunc(func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, i.unaryTimeout)
		defer cancel()
		return next(ctx, req)
	})
}

func (*deadlineInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *deadlineInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return connect.StreamingHandlerFunc(func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(i.shutdownCtx, cancel)
		defer stop()
		return next(ctx, conn)
	})
}
