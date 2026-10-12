package cmdutil

import (
	"context"
	"log/slog"

	"connectrpc.com/connect/v2"
)

// LogRequestID extracts and logs the X-Loco-Request-Id only if err is not nil.
// This helps with debugging API errors by correlating with server-side logs.
func LogRequestID(ctx context.Context, err error, msg string) {
	if err == nil {
		return
	}

	const requestIDHeaderName = "X-Loco-Request-Id"
	var headerValue string

	if info, ok := connect.CallInfoForClientContext(ctx); ok {
		headerValue = info.ResponseHeader().Get(requestIDHeaderName)
	}

	slog.ErrorContext(ctx, msg, requestIDHeaderName, headerValue, "error", err)
}
