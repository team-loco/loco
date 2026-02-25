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
	// API tokens
	MaxAPITokenDuration time.Duration // maximum allowed duration when issuing an API token

	// Session tokens
	SessionAccessTokenDuration  time.Duration // duration of the access token issued at login
	SessionRefreshTokenDuration time.Duration // duration of the refresh token issued at login

	// How often last_used_at is written to the DB (throttles writes on the hot path)
	LastUsedUpdateInterval time.Duration
}

// NewVendingMachine creates a new VendingMachine with the given database pool, queries, and configuration.
// Starts a background goroutine to periodically clean up expired tokens.
// Call Close() to stop the background cleanup goroutine.
func NewVendingMachine(pool *pgxpool.Pool, q queries.Querier, cfg Config) *VendingMachine {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := q.DeleteExpiredSessionTokens(ctx); err != nil {
					slog.ErrorContext(ctx, "failed to delete expired session tokens", "err", err)
				}
				if err := q.DeleteExpiredAPITokens(ctx); err != nil {
					slog.ErrorContext(ctx, "failed to delete expired api tokens", "err", err)
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
