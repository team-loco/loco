package interceptor

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
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

func (i *deadlineInterceptor) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if spec.StreamType == connect.StreamTypeUnary {
				unaryCtx, cancel := context.WithTimeout(ctx, i.unaryTimeout)
				defer cancel()
				return next(unaryCtx, spec, stream)
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(i.shutdownCtx, cancel)
			defer stop()
			return next(ctx, spec, stream)
		}
	}
}
