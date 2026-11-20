package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/db/dbtest"
)

const deadlockWait = 200 * time.Millisecond

func TestWithTxRunsATransactionAgainAfterADeadlock(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	ctx := t.Context()
	firstHeld, otherHeld := make(chan struct{}), make(chan struct{})
	otherDone := make(chan error, 1)
	go func() {
		<-firstHeld
		tx, err := pool.Begin(context.Background())
		if err != nil {
			otherDone <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(2)"); err != nil {
			otherDone <- err
			return
		}
		close(otherHeld)
		time.Sleep(deadlockWait)
		if _, err = tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock(1)"); err != nil {
			otherDone <- err
			return
		}
		otherDone <- tx.Commit(context.Background())
	}()

	attempts := 0
	err := inTx(ctx, pool, func(tx pgx.Tx) error {
		attempts++
		if _, lockErr := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(1)"); lockErr != nil {
			return lockErr
		}
		if attempts == 1 {
			close(firstHeld)
			<-otherHeld
		}
		_, lockErr := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2)")
		return lockErr
	})
	if err != nil {
		t.Fatalf("inTx = %v after %d attempts", err, attempts)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if err = <-otherDone; err != nil {
		t.Fatalf("the other transaction: %v", err)
	}
}
