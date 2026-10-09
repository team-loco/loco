package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func bumpEnvironmentRevision(ctx context.Context, qtx *genDb.Queries, environmentID uuid.UUID) error {
	if err := qtx.BumpEnvironmentRevision(ctx, environmentID); err != nil {
		return fmt.Errorf("bump environment revision: %w", err)
	}
	return nil
}

func bumpResourceEnvironmentRevisions(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	if err := qtx.BumpResourceEnvironmentRevisions(ctx, resourceID); err != nil {
		return fmt.Errorf("bump environment revisions: %w", err)
	}
	return nil
}
