package interceptor

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

const (
	requestIDHeader      = "X-Loco-Request-Id"
	serverTimingHeader   = "Server-Timing"
	callerRequestID      = "caller-request-id"
	contextTestProcedure = "/loco.test.v1.TestService/Call"
)

type headerStreamConn struct {
	connect.StreamingHandlerConn
	requestHeader   http.Header
	responseHeader  http.Header
	responseTrailer http.Header
}

func newHeaderStreamConn() *headerStreamConn {
	requestHeader := http.Header{}
	requestHeader.Set(requestIDHeader, callerRequestID)
	return &headerStreamConn{
		requestHeader:   requestHeader,
		responseHeader:  http.Header{},
		responseTrailer: http.Header{},
	}
}

func (*headerStreamConn) Spec() connect.Spec {
	return connect.Spec{Procedure: contextTestProcedure}
}

func (c *headerStreamConn) RequestHeader() http.Header {
	return c.requestHeader
}

func (c *headerStreamConn) ResponseHeader() http.Header {
	return c.responseHeader
}

func (c *headerStreamConn) ResponseTrailer() http.Header {
	return c.responseTrailer
}

func TestContextInterceptorUnaryResponseHeaders(t *testing.T) {
	next := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&struct{}{}), nil
	}
	wrapped := NewContextInterceptor().WrapUnary(next)
	req := connect.NewRequest(&struct{}{})
	req.Header().Set(requestIDHeader, callerRequestID)

	resp, err := wrapped(t.Context(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := resp.Header().Get(requestIDHeader); got != callerRequestID {
		t.Errorf("%s = %q, want %q", requestIDHeader, got, callerRequestID)
	}
	if got := resp.Header().Values(serverTimingHeader); len(got) != 0 {
		t.Errorf("%s = %q, want none", serverTimingHeader, got)
	}
}

func TestContextInterceptorStreamResponseMetadata(t *testing.T) {
	next := func(_ context.Context, _ connect.StreamingHandlerConn) error {
		return nil
	}
	wrapped := NewContextInterceptor().WrapStreamingHandler(next)
	conn := newHeaderStreamConn()

	if err := wrapped(t.Context(), conn); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := conn.responseHeader.Get(requestIDHeader); got != callerRequestID {
		t.Errorf("%s = %q, want %q", requestIDHeader, got, callerRequestID)
	}
	if got := conn.responseHeader.Values(serverTimingHeader); len(got) != 0 {
		t.Errorf("header %s = %q, want none", serverTimingHeader, got)
	}
	if got := conn.responseTrailer.Values(serverTimingHeader); len(got) != 0 {
		t.Errorf("trailer %s = %q, want none", serverTimingHeader, got)
	}
}
