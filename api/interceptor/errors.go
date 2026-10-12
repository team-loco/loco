package interceptor

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"connectrpc.com/connect/v2"
)

const (
	publicRequestFailed    = "request failed"
	publicRequestCanceled  = "request canceled"
	publicDeadlineExceeded = "request deadline exceeded"
)

func NewErrorInterceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			err := next(ctx, spec, stream)
			if err == nil {
				return nil
			}
			if connectErr, ok := errors.AsType[*connect.Error](err); ok {
				return connectErr
			}
			if errors.Is(err, io.EOF) {
				return err
			}
			if errors.Is(err, context.Canceled) {
				return connect.NewError(connect.CodeCanceled, publicRequestCanceled).WithCause(err)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return connect.NewError(connect.CodeDeadlineExceeded, publicDeadlineExceeded).WithCause(err)
			}
			slog.ErrorContext(ctx, "handler failed", "procedure", spec.Procedure, "error", err)
			return connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
		}
	}
}
