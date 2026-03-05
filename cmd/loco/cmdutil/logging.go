package cmdutil

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
)

// LogRequestID extracts and logs the X-Loco-Request-Id only if err is not nil.
// This helps with debugging API errors by correlating with server-side logs.
func LogRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string

	if cErr, ok := errors.AsType[*connect.Error](err); ok {
		headerValue = cErr.Meta().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}
