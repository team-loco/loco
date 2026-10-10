package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func bumpEnvironmentRevision(ctx context.Context, qtx *genDb.Queries, environmentID uuid.UUID) (int64, error) {
	revision, err := qtx.BumpEnvironmentRevision(ctx, environmentID)
	if err != nil {
		return 0, fmt.Errorf("bump environment revision: %w", err)
	}
	return revision, nil
}
