package interceptor

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
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
			// Only an error the handler built itself is public. One found deeper in the chain, such
			// as an outbound RPC's, carries another server's message.
			if connectErr, ok := err.(*connect.Error); ok { //nolint:errorlint // see above
				return connectErr
			}
			if public, ok := rpcerr.Public(err); ok {
				return public
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
