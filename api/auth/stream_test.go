package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracingtest"
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

func TestGuardStreamReportsCredentialRemovedDuringPermissionCheck(t *testing.T) {
	t.Parallel()
	var removed atomic.Bool
	gone := connect.NewError(connect.CodeUnauthenticated, "credential removed")
	ctx := WithStreamValidation(t.Context(), StreamConfig{Interval: time.Millisecond, Timeout: time.Second},
		func(context.Context) (reqctx.Principal, error) {
			if removed.Load() {
				return reqctx.Principal{}, gone
			}
			return reqctx.Principal{}, nil
		})
	var checks atomic.Int32
	ctx, stop, err := GuardStream(ctx, func(context.Context) error {
		if checks.Add(1) == 1 {
			return nil
		}
		// The credential disappears after this check read it.
		removed.Store(true)
		return connect.NewError(connect.CodePermissionDenied, "permission denied")
	})
	if err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	if err = stop(); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("stream failure = %v, want unauthenticated", err)
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

func TestGuardStreamTracesEachRecheckAsARootLinkedToTheStream(t *testing.T) {
	recorder := tracingtest.Record(t)
	ctx := tracingtest.StartParent(t)
	denied := connect.NewError(connect.CodePermissionDenied, "permission denied")
	ctx = WithStreamValidation(ctx, StreamConfig{Interval: time.Hour, Timeout: time.Second},
		func(context.Context) (reqctx.Principal, error) { return reqctx.Principal{}, nil })
	if _, _, err := GuardStream(ctx, func(context.Context) error { return denied }); err == nil {
		t.Fatal("stream opened after a denied check")
	}
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != streamRecheckSpan {
		t.Fatalf("ended spans = %v, want one %s", ended, streamRecheckSpan)
	}
	span := ended[0]
	if span.Parent().IsValid() {
		t.Fatalf("recheck span has parent %v", span.Parent())
	}
	stream := trace.SpanContextFromContext(ctx)
	if links := span.Links(); len(links) != 1 || !links[0].SpanContext.Equal(stream) {
		t.Fatalf("recheck links = %v, want the stream span %v", links, stream)
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("recheck status = %+v, want an error", span.Status())
	}
}

func TestGuardStreamEndsARecheckCancelledByStopWithoutError(t *testing.T) {
	recorder := tracingtest.Record(t)
	var checks atomic.Int32
	started := make(chan struct{})
	ctx := WithStreamValidation(t.Context(), StreamConfig{Interval: time.Millisecond, Timeout: time.Hour},
		func(ctx context.Context) (reqctx.Principal, error) {
			if checks.Add(1) == 2 {
				close(started)
				<-ctx.Done()
				return reqctx.Principal{}, ctx.Err()
			}
			return reqctx.Principal{}, nil
		})
	_, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = stop(); err != nil {
		t.Fatalf("stop = %v, want a clean end", err)
	}
	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("ended spans = %d, want the initial and the stopped recheck", len(ended))
	}
	for _, span := range ended {
		if span.Status().Code == codes.Error {
			t.Fatalf("%s status = %+v, want no error", span.Name(), span.Status())
		}
	}
}
