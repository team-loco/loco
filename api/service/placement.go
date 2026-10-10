package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func placeApplication(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.UpsertPlacementParams,
) (genDb.UpsertPlacementRow, error) {
	placement, err := qtx.UpsertPlacement(ctx, params)
	if err != nil {
		return genDb.UpsertPlacementRow{}, fmt.Errorf("upsert placement: %w", err)
	}
	clusterID := params.ClusterID.String()
	if err := qtx.NotifyClusterPlacements(ctx, clusterID); err != nil {
		return genDb.UpsertPlacementRow{}, fmt.Errorf("notify placements: %w", err)
	}
	slog.InfoContext(ctx, "placement updated",
		"placement_id", placement.ID,
		"revision", placement.DesiredRevision,
		"cluster_id", params.ClusterID,
		"resource_id", params.ResourceID,
	)
	return placement, nil
}

func removePlacement(ctx context.Context, qtx *genDb.Queries, resourceID, clusterID uuid.UUID) error {
	removed, err := qtx.MarkPlacementDeleted(ctx, genDb.MarkPlacementDeletedParams{
		ResourceID: resourceID,
		ClusterID:  clusterID,
	})
	if err != nil {
		return fmt.Errorf("mark placement deleted: %w", err)
	}
	return notifyRemovedPlacements(ctx, qtx, resourceID, removed)
}

func removeResourcePlacements(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	rows, err := qtx.MarkResourcePlacementsDeleted(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("mark resource placements deleted: %w", err)
	}
	removed := make([]genDb.MarkPlacementDeletedRow, 0, len(rows))
	for _, row := range rows {
		removed = append(removed, genDb.MarkPlacementDeletedRow(row))
	}
	return notifyRemovedPlacements(ctx, qtx, resourceID, removed)
}

func notifyRemovedPlacements(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	removed []genDb.MarkPlacementDeletedRow,
) error {
	for _, placement := range removed {
		clusterID := placement.ClusterID.String()
		if err := qtx.NotifyClusterPlacements(ctx, clusterID); err != nil {
			return fmt.Errorf("notify placements: %w", err)
		}
		slog.InfoContext(ctx, "placement marked for deletion",
			"placement_id", placement.ID,
			"revision", placement.DesiredRevision,
			"cluster_id", placement.ClusterID,
			"resource_id", resourceID,
		)
	}
	return nil
}

func rollPlacementsForSecrets(ctx context.Context, qtx *genDb.Queries, environmentID uuid.UUID, names []string) error {
	rolled, err := qtx.BumpPlacementsForSecretNames(ctx, genDb.BumpPlacementsForSecretNamesParams{
		EnvironmentID: environmentID,
		Names:         names,
	})
	if err != nil {
		return fmt.Errorf("bump placements for secrets: %w", err)
	}
	notified := make(map[uuid.UUID]struct{}, len(rolled))
	for _, placement := range rolled {
		slog.InfoContext(ctx, "placement rolled for a secret change",
			"placement_id", placement.ID,
			"revision", placement.DesiredRevision,
			"cluster_id", placement.ClusterID,
		)
		if _, done := notified[placement.ClusterID]; done {
			continue
		}
		notified[placement.ClusterID] = struct{}{}
		clusterID := placement.ClusterID.String()
		if err := qtx.NotifyClusterPlacements(ctx, clusterID); err != nil {
			return fmt.Errorf("notify placements: %w", err)
		}
	}
	return nil
}
