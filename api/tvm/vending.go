package tvm

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	queries "github.com/team-loco/loco/api/gen/db"
)

type VendingMachine struct {
	queries queries.Querier
	authz   *authz.Authorizer
	Cfg     Config
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
func NewVendingMachine(pool *pgxpool.Pool, q queries.Querier, cfg Config) *VendingMachine {
	return &VendingMachine{
		queries: q,
		authz:   authz.New(pool, q),
		Cfg:     cfg,
	}
}

func (tvm *VendingMachine) WithQueries(q queries.Querier) *VendingMachine {
	scoped := *tvm
	scoped.queries = q
	return &scoped
}

// RunCleanup deletes expired session and API tokens every interval until ctx is canceled.
func (tvm *VendingMachine) RunCleanup(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := tvm.queries.DeleteExpiredSessionTokens(ctx); err != nil {
				slog.ErrorContext(ctx, "failed to delete expired session tokens", "err", err)
			}
			if err := tvm.queries.DeleteExpiredAPITokens(ctx); err != nil {
				slog.ErrorContext(ctx, "failed to delete expired api tokens", "err", err)
			}
		}
	}
}
