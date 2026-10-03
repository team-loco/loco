package interceptor

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
)

type fakeStreamConn struct {
	connect.StreamingHandlerConn
}

func TestDeadlineInterceptorUnaryTimeout(t *testing.T) {
	ic := NewDeadlineInterceptor(context.Background(), 10*time.Millisecond)
	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	wrapped := ic.WrapUnary(next)
	req := connect.NewRequest(&struct{}{})
	_, err := wrapped(context.Background(), req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDeadlineInterceptorCancelsStreamsOnShutdown(t *testing.T) {
	shutdownCtx, shutdown := context.WithCancel(context.Background())
	ic := NewDeadlineInterceptor(shutdownCtx, time.Second)
	started := make(chan struct{})
	next := func(ctx context.Context, _ connect.StreamingHandlerConn) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	wrapped := ic.WrapStreamingHandler(next)
	errCh := make(chan error, 1)
	go func() {
		errCh <- wrapped(context.Background(), fakeStreamConn{})
	}()
	<-started
	shutdown()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream was not canceled on shutdown")
	}
}
