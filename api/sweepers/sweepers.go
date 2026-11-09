// Package sweepers holds the background jobs that reclaim build sources, images and registry credentials.
package sweepers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	sweepRanAttr          = "loco.sweep.ran"
	advisoryUnlockTimeout = 5 * time.Second
)

// runEvery calls sweep at once and then every interval until ctx is done.
func runEvery(ctx context.Context, interval time.Duration, sweep func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runLocked calls fn while holding the advisory lock, so one replica sweeps at a time.
// It reports false without calling fn when another replica holds the lock.
func runLocked(ctx context.Context, pool *pgxpool.Pool, lockKey int64, fn func() error) (bool, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("acquire connection: %w", err)
	}
	lockQueries := genDb.New(conn)
	locked, err := lockQueries.TryAdvisoryLock(ctx, lockKey)
	if err != nil {
		conn.Release()
		return false, fmt.Errorf("lock %#x: %w", lockKey, err)
	}
	if !locked {
		conn.Release()
		return false, nil
	}
	defer unlockAdvisory(ctx, conn, lockQueries, lockKey)
	return true, fn()
}

func unlockAdvisory(ctx context.Context, conn *pgxpool.Conn, lockQueries *genDb.Queries, lockKey int64) {
	detached := context.WithoutCancel(ctx)
	unlockCtx, cancel := context.WithTimeout(detached, advisoryUnlockTimeout)
	defer cancel()
	unlocked, err := lockQueries.AdvisoryUnlock(unlockCtx, lockKey)
	if err == nil && unlocked {
		conn.Release()
		return
	}
	slog.WarnContext(ctx, "failed to release an advisory lock, closing its connection",
		"lockKey", lockKey,
		"error", err,
	)
	raw := conn.Hijack()
	if closeErr := raw.Close(unlockCtx); closeErr != nil {
		slog.WarnContext(ctx, "failed to close the advisory lock connection", "lockKey", lockKey, "error", closeErr)
	}
}
