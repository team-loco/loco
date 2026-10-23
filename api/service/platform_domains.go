package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
)

const platformDomainSyncLockKey int64 = 0x6c6f636f0003

func SyncPlatformDomains(ctx context.Context, pool *pgxpool.Pool, domains []string) error {
	return withTx(ctx, pool, func(qtx *genDb.Queries) error {
		if err := qtx.AdvisoryXactLock(ctx, platformDomainSyncLockKey); err != nil {
			return fmt.Errorf("lock platform domains: %w", err)
		}
		for _, domain := range domains {
			if err := qtx.EnsurePlatformDomain(ctx, domain); err != nil {
				return fmt.Errorf("ensure platform domain %s: %w", domain, err)
			}
		}
		return nil
	})
}
