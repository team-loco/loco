package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const (
	tailLoopCancelDelay = 100 * time.Millisecond
	tailLoopReturnLimit = 500 * time.Millisecond
)

func noopTailSend(*observabilityv1.TailLogsResponse) error { return nil }

func TestTailLoopReturnsPromptlyWhenCanceledBetweenPolls(t *testing.T) {
	service := NewObservabilityService(&fakeStore{}, &config.Config{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(tailLoopCancelDelay, cancel)

	start := time.Now()
	err := service.tailLoop(ctx, &observabilityv1.TailLogsRequest{}, time.Now(), noopTailSend)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("canceled tail = %v, want nil", err)
	}
	if elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
}

func TestTailLoopEndsTheStreamWhenAPollFails(t *testing.T) {
	store := &fakeStore{err: errTraceTestStorage}
	service := NewObservabilityService(store, &config.Config{}, nil)

	start := time.Now()
	err := service.tailLoop(t.Context(), &observabilityv1.TailLogsRequest{}, time.Now(), noopTailSend)
	elapsed := time.Since(start)

	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("failed poll = %v, want an internal error", err)
	}
	if elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}
