package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Loco-Request-Id"

// ResolveRequestID keeps the caller's request ID so one ID follows a request across services.
func ResolveRequestID(existing string) string {
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

type requestIDKey struct{}

// WithRequestID records the ID of the request ctx serves, which outgoing calls forward.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey{}).(string)
	return id, ok
}

// RequestIDInterceptor forwards the request ID of the request ctx serves, and logs a failed
// call's ID at DEBUG.
func RequestIDInterceptor(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
		if id, ok := RequestIDFrom(ctx); ok && id != "" {
			// Client calls always attach CallInfo before running interceptors.
			info, _ := connect.CallInfoForClientContext(ctx)
			info.RequestHeader().Set(RequestIDHeader, id)
		}
		stream, err := next(ctx, spec)
		if err != nil {
			return nil, err
		}
		return &requestIDStream{ClientStream: stream, ctx: ctx, procedure: spec.Procedure}, nil
	}
}

type requestIDStream struct {
	connect.ClientStream
	ctx       context.Context
	procedure string
	once      sync.Once
}

func (s *requestIDStream) Send(msg any) error {
	return s.log(s.ClientStream.Send(msg))
}

func (s *requestIDStream) Receive(msg any) error {
	return s.log(s.ClientStream.Receive(msg))
}

func (s *requestIDStream) log(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}
	s.once.Do(func() {
		var requestID string
		if info, ok := connect.CallInfoForClientContext(s.ctx); ok {
			requestID = info.ResponseHeader().Get(RequestIDHeader)
		}
		slog.DebugContext(s.ctx, "rpc failed", "procedure", s.procedure, "request_id", requestID, "error", err)
	})
	return err
}
