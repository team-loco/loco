package tvm

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	queries "github.com/team-loco/loco/api/gen/db"
)

type VendingMachine struct {
	pool       *pgxpool.Pool
	queries    queries.Querier
	Cfg        Config
	cancelFunc context.CancelFunc
}

type Config struct {
	MaxTokenDuration   time.Duration
	LoginTokenDuration time.Duration
}

// NewVendingMachine creates a new VendingMachine with the given database pool, queries, and configuration.
// starts a background goroutine to periodically clean up expired tokens (in-process cron)
// Close() to stop the background cleanup goroutine and release resources.
func NewVendingMachine(pool *pgxpool.Pool, q queries.Querier, cfg Config) *VendingMachine {
	ctx, cancel := context.WithCancel(context.Background())

	// low cost pg cron
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := q.DeleteExpiredTokens(ctx); err != nil {
					slog.ErrorContext(ctx, err.Error())
				}
			}
		}
	}()

	return &VendingMachine{
		pool:       pool,
		queries:    q,
		Cfg:        cfg,
		cancelFunc: cancel,
	}
}

// Close stops the background cleanup goroutine.
func (tvm *VendingMachine) Close() {
	if tvm.cancelFunc != nil {
		tvm.cancelFunc()
	}
}
