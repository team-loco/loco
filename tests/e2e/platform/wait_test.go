package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPollReportsTheLastAttemptTheDeadlineDidNotCut(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), pollInterval+pollInterval/10)
	defer cancel()
	attempts := 0
	err := Poll(ctx, func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("the old revision still serves")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "the old revision still serves") {
		t.Fatalf("Poll returned %v, want the state the last finished attempt saw", err)
	}
}
