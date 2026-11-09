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

func noopTailSend(*observabilityv1.LogEntry) error { return nil }

func noopHeartbeat(*observabilityv1.Heartbeat) error { return nil }

func TestTailLoopReturnsPromptlyWhenCanceledBetweenPolls(t *testing.T) {
	service := NewObservabilityService(newTestClickHouse(t), &config.Config{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(tailLoopCancelDelay, cancel)

	start := time.Now()
	err := service.tailLoop(
		ctx,
		tailLogQuery(&observabilityv1.TailLogsRequest{}, time.Now()),
		noopTailSend,
		noopHeartbeat,
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("canceled tail = %v, want nil", err)
	}
	if elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
}

func TestTailLoopEndsTheStreamWhenAPollFails(t *testing.T) {
	service := NewObservabilityService(unavailableClickHouse(t), &config.Config{}, nil)

	start := time.Now()
	err := service.tailLoop(
		t.Context(),
		tailLogQuery(&observabilityv1.TailLogsRequest{}, time.Now()),
		noopTailSend,
		noopHeartbeat,
	)
	elapsed := time.Since(start)

	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("failed poll = %v, want an internal error", err)
	}
	if elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
}
