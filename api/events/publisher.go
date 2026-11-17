package events

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	publishBatchSize = 500
	publishInterval  = time.Second
)

// RunPublisher assigns durable stream positions until ctx is canceled.
func RunPublisher(ctx context.Context, pool *pgxpool.Pool) {
	ticker := time.NewTicker(publishInterval)
	defer ticker.Stop()
	for {
		if err := Publish(ctx, pool); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "publish events", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Publish assigns consecutive positions to one batch of committed events.
func Publish(ctx context.Context, pool *pgxpool.Pool) error {
	err := pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		position, err := q.LockEventStreamCounter(ctx)
		if err != nil {
			return err
		}
		_, err = q.PublishEvents(ctx, genDb.PublishEventsParams{Position: position, BatchSize: publishBatchSize})
		return err
	})
	if err != nil {
		return fmt.Errorf("assign event stream positions: %w", err)
	}
	return nil
}
