package platform

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const pollInterval = 2 * time.Second

// Eventually calls check until it returns nil, and fails the test with check's last error once
// timeout passes or the test ends.
func Eventually(tb testing.TB, timeout time.Duration, what string, check func(ctx context.Context) error) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), timeout)
	defer cancel()
	if err := Poll(ctx, check); err != nil {
		tb.Fatalf("%s within %s: %v", what, timeout, err)
	}
}

// Poll calls check every pollInterval until it returns nil or ctx ends, and then returns check's
// last error.
func Poll(ctx context.Context, check func(ctx context.Context) error) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		err := check(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last: %w)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

// waitFor polls check during setup, bounded by timeout.
func waitFor(ctx context.Context, timeout time.Duration, what string, check func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := Poll(ctx, check); err != nil {
		return fmt.Errorf("wait for %s: %w", what, err)
	}
	return nil
}
