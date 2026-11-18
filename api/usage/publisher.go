package usage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
)

const publishInterval = time.Second

func RunPublisher(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(publishInterval)
	defer ticker.Stop()
	for {
		if err := Publish(ctx, pool); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "publish usage", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func Publish(ctx context.Context, pool *pgxpool.Pool) error {
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		position, err := q.LockUsageStreamCounter(ctx)
		if err != nil {
			return err
		}
		if err = q.PublishClusterUsageReports(ctx); err != nil {
			return err
		}
		if position, err = q.PublishWorkloadRuns(ctx, position); err != nil {
			return err
		}
		if position, err = q.PublishEgressUsage(ctx, position); err != nil {
			return err
		}
		if err = q.UpdateUsageStreamCounter(ctx, position); err != nil {
			return err
		}
		return q.PruneClusterUsageReports(ctx)
	})
	if err != nil {
		return fmt.Errorf("assign usage stream positions: %w", err)
	}
	return nil
}
