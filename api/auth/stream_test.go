package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-loco/loco/api/reqctx"
)

func TestGuardStreamEndsIdleStreamsOnCredentialOrPermissionLoss(t *testing.T) {
	t.Parallel()
	for _, target := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential", true: "target permission"}[target], func(t *testing.T) {
			var rejected atomic.Bool
			want := errors.New("access removed")
			ctx := WithStreamValidation(t.Context(), StreamConfig{Interval: time.Millisecond, Timeout: time.Second},
				func(context.Context) (reqctx.Principal, error) {
					if !target && rejected.Load() {
						return reqctx.Principal{}, want
					}
					return reqctx.Principal{}, nil
				})
			ctx, stop, err := GuardStream(ctx, func(context.Context) error {
				if target && rejected.Load() {
					return want
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rejected.Store(true)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("idle stream kept access after revocation")
			}
			if err = stop(); !errors.Is(err, want) {
				t.Fatalf("stream failure = %v, want access removed", err)
			}
		})
	}
}

func TestGuardStreamBoundsAndJoinsValidation(t *testing.T) {
	t.Parallel()
	var checks atomic.Int32
	exited := make(chan struct{})
	ctx := WithStreamValidation(t.Context(), StreamConfig{Interval: time.Millisecond, Timeout: time.Millisecond},
		func(ctx context.Context) (reqctx.Principal, error) {
			if checks.Add(1) == 1 {
				return reqctx.Principal{}, nil
			}
			<-ctx.Done()
			close(exited)
			return reqctx.Principal{}, ctx.Err()
		})
	ctx, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("a blocked validation did not end the stream")
	}
	if err = stop(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream failure = %v, want validation deadline", err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("validator is still running after stop")
	}
}

func TestGuardStreamRequiresAnAuthenticatedValidator(t *testing.T) {
	t.Parallel()
	if _, _, err := GuardStream(t.Context(), func(context.Context) error { return nil }); err == nil {
		t.Fatal("stream opened without credential validation")
	}
}

func TestGuardStreamStopsWithoutChangingClientCancellation(t *testing.T) {
	t.Parallel()
	ctx := WithStreamValidation(t.Context(), StreamConfig{Interval: time.Hour, Timeout: time.Second},
		func(context.Context) (reqctx.Principal, error) { return reqctx.Principal{}, nil })
	_, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = stop(); err != nil {
		t.Fatalf("normal stream shutdown = %v", err)
	}
}
