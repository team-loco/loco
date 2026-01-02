package tvm

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	queries "github.com/team-loco/loco/api/gen/db"
)

type VendingMachine struct {
	pool    *pgxpool.Pool
	queries queries.Querier
	cfg     Config
}

type Config struct {
	MaxTokenDuration   time.Duration
	LoginTokenDuration time.Duration
}

// NewVendingMachine creates a new VendingMachine with the given database pool, queries, and configuration.
func NewVendingMachine(pool *pgxpool.Pool, q queries.Querier, cfg Config) *VendingMachine {
	// low cost pg cron
	go func() {
		ctx := context.Background()
		for range time.Tick(time.Minute) {
			q.DeleteExpiredTokens(ctx)
		}
	}()
	return &VendingMachine{
		pool:    pool,
		queries: q,
		cfg:     cfg,
	}
}
