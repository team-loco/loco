package interceptor

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
)

func TestDeadlineInterceptorUnaryTimeout(t *testing.T) {
	ic := NewDeadlineInterceptor(t.Context(), 10*time.Millisecond)
	next := func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
		<-ctx.Done()
		return ctx.Err()
	}
	wrapped := ic.Interceptor()(next)
	err := wrapped(t.Context(), connect.Spec{StreamType: connect.StreamTypeUnary}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestDeadlineInterceptorCancelsStreamsOnShutdown(t *testing.T) {
	for _, streamType := range []connect.StreamType{
		connect.StreamTypeClient, connect.StreamTypeServer, connect.StreamTypeBidi,
	} {
		t.Run(streamType.String(), func(t *testing.T) {
			shutdownCtx, shutdown := context.WithCancel(t.Context())
			defer shutdown()
			ic := NewDeadlineInterceptor(shutdownCtx, time.Second)
			started := make(chan struct{})
			next := func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}
			wrapped := ic.Interceptor()(next)
			errCh := make(chan error, 1)
			go func() { errCh <- wrapped(t.Context(), connect.Spec{StreamType: streamType}, nil) }()
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
		})
	}
}
