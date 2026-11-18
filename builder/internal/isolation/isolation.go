// Package isolation checks that the build pod's network policy is in effect.
package isolation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

const (
	dialTimeout = time.Second
	retryDelay  = 250 * time.Millisecond
	// filteredDials is how many dials in a row must go unanswered, since one can time out on a busy node.
	filteredDials = 2
)

var errNotIsolated = errors.New("the build's network policy is not in effect: " +
	"builds need a network plugin that enforces Kubernetes NetworkPolicy")

// Wait returns once dials to address, which the build's network policy forbids, are filtered.
// A connection or a refusal means packets still reach address, so Wait keeps trying until timeout.
func Wait(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	filtered := 0
	var last error
	for filtered < filteredDials {
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s still answered after %s (%w)", errNotIsolated, address, timeout, last)
		}
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		last = dial(dialCtx, address)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		if isFiltered(last) {
			filtered++
			continue
		}
		filtered = 0
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	return nil
}

func dial(ctx context.Context, address string) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close connection to %s: %w", address, err)
	}
	return errConnected
}

var errConnected = errors.New("connected")

func isFiltered(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EPERM)
}
