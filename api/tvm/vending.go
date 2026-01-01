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

// Revoke deletes the given token, effectively immediately revoking it.
func (tvm *VendingMachine) Revoke(ctx context.Context, token string) error {
	return tvm.queries.DeleteToken(ctx, token)
}

// ListTokensForEntity lists all tokens associated with the given entity. This function does not check the permissions of the caller.
// It is expected that the caller has already verified that the caller has sufficient permissions to list the tokens for the given entity.
func (tvm *VendingMachine) ListTokensForEntity(ctx context.Context, entity queries.Entity) ([]queries.TokenHead, error) {
	return tvm.queries.ListTokensForEntity(ctx, queries.ListTokensForEntityParams{
		EntityType: entity.Type,
		EntityID:   entity.ID,
	})
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
