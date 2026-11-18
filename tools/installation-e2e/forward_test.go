package main

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryForwardWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitRegistry(ctx, "http://127.0.0.1:1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readiness cancellation: %v", err)
	}
}
