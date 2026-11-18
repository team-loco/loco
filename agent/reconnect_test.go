package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
)

const (
	testReconnectDelay      = time.Millisecond
	testHealthyStreamWindow = time.Hour
	testAttemptsBeforeStop  = 2
	testClusterRemoved      = "cluster is removed"
	testCredentialRejected  = "invalid agent credential"
	testMessageTooLarge     = "message size 4194305 is larger than configured max 4194304"
)

var errTestStreamDropped = errors.New("stream dropped")

func TestReconnectLoopStopsOnAnErrorARetryCannotFix(t *testing.T) {
	permanent := map[string]error{
		"permission denied":  connect.NewError(connect.CodePermissionDenied, testClusterRemoved),
		"unauthenticated":    connect.NewError(connect.CodeUnauthenticated, testCredentialRejected),
		"resource exhausted": connect.NewError(connect.CodeResourceExhausted, testMessageTooLarge),
	}
	for name, rejection := range permanent {
		t.Run(name, func(t *testing.T) {
			a := &Agent{cfg: &Config{
				ReconnectBaseDelay:    testReconnectDelay,
				ReconnectMaxDelay:     testReconnectDelay,
				HealthyStreamDuration: testHealthyStreamWindow,
			}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			attempts := 0
			err := a.reconnectLoop(ctx, func(context.Context) error {
				attempts++
				if attempts == testAttemptsBeforeStop {
					cancel()
				}
				return rejection
			})
			if !errors.Is(err, rejection) {
				t.Fatalf("reconnectLoop returned %v, want %v", err, rejection)
			}
			if attempts != 1 {
				t.Fatalf("reconnectLoop made %d attempts, want 1", attempts)
			}
		})
	}
}

func TestReconnectLoopRetriesADroppedStream(t *testing.T) {
	a := &Agent{cfg: &Config{
		ReconnectBaseDelay:    testReconnectDelay,
		ReconnectMaxDelay:     testReconnectDelay,
		HealthyStreamDuration: testHealthyStreamWindow,
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempts := 0
	err := a.reconnectLoop(ctx, func(context.Context) error {
		attempts++
		if attempts == testAttemptsBeforeStop {
			cancel()
		}
		return errTestStreamDropped
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reconnectLoop returned %v, want %v", err, context.Canceled)
	}
	if attempts != testAttemptsBeforeStop {
		t.Fatalf("reconnectLoop made %d attempts, want %d", attempts, testAttemptsBeforeStop)
	}
}
