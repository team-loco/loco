package loco

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
)

// logRequestID extracts and logs the X-Loco-Request-Id only if err is not nil.
// This helps with debugging API errors by correlating with server-side logs.
func logRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string
	var cErr *connect.Error

	if errors.As(err, &cErr) {
		headerValue = cErr.Meta().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}
