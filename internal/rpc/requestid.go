package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"connectrpc.com/connect/v2"
)

const RequestIDHeader = "X-Loco-Request-Id"

func requestIDInterceptor(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
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
