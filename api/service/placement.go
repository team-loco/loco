package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var errPlacementApplicationMissing = errors.New("placement application spec is missing")

func placeApplication(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.UpsertPlacementParams,
) error {
	placement, err := qtx.UpsertPlacement(ctx, params)
	if err != nil {
		return fmt.Errorf("upsert placement: %w", err)
	}
	window := genDb.OpenPlacementWindowParams{ResourceID: params.ResourceID, ClusterID: params.ClusterID}
	if err := qtx.OpenPlacementWindow(ctx, window); err != nil {
		return fmt.Errorf("open placement window: %w", err)
	}
	clusterID := params.ClusterID.String()
	if err := qtx.NotifyClusterPlacements(ctx, clusterID); err != nil {
		return fmt.Errorf("notify placements: %w", err)
	}
	slog.InfoContext(ctx, "placement updated",
		"placement_id", placement.ID,
		"revision", placement.DesiredRevision,
		"cluster_id", params.ClusterID,
		"resource_id", params.ResourceID,
	)
	return nil
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
		window := genDb.ClosePlacementWindowParams{ResourceID: resourceID, ClusterID: placement.ClusterID}
		if err := qtx.ClosePlacementWindow(ctx, window); err != nil {
			return fmt.Errorf("close placement window: %w", err)
		}
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

func rollPlacementsForSecrets(ctx context.Context, qtx *genDb.Queries, target secretTarget, names []string) error {
	rolled, err := qtx.BumpPlacementsForSecretNames(ctx, genDb.BumpPlacementsForSecretNamesParams{
		ResourceID:    target.resourceID,
		EnvironmentID: target.env.ID,
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

func desiredEnv(
	ctx context.Context,
	q genDb.Querier,
	resourceID, clusterID uuid.UUID,
) (map[string]string, error) {
	placement, err := q.GetPlacementForResourceCluster(ctx, genDb.GetPlacementForResourceClusterParams{
		ResourceID: resourceID,
		ClusterID:  clusterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get placement: %w", err)
	}
	if placement.DesiredDeleted || len(placement.DesiredSpec) == 0 {
		return nil, nil
	}

	payload, err := decodePlacement(placement)
	if err != nil {
		return nil, err
	}
	if payload.AppSpec.ServiceSpec == nil || payload.AppSpec.ServiceSpec.Deployment == nil {
		return nil, nil
	}
	return payload.AppSpec.ServiceSpec.Deployment.Env, nil
}

func renamePlacements(ctx context.Context, qtx *genDb.Queries, resource genDb.Resource) error {
	environments, err := qtx.ListWorkspaceEnvironments(ctx, resource.WorkspaceID)
	if err != nil {
		return fmt.Errorf("list rename environments: %w", err)
	}
	for _, environment := range environments {
		key := genDb.ListResourceEnvironmentPlacementsParams{ResourceID: resource.ID, EnvironmentID: environment.ID}
		placements, err := qtx.ListResourceEnvironmentPlacements(ctx, key)
		if err != nil {
			return fmt.Errorf("list rename placements: %w", err)
		}
		for _, placement := range placements {
			payload, err := decodePlacement(placement)
			if err != nil {
				return err
			}
			if payload.AppSpec.Slug == resource.Slug {
				continue
			}
			payload.AppSpec.Slug = resource.Slug
			if err := rewritePlacement(ctx, qtx, placement, payload); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodePlacement(placement genDb.Placement) (locoControllerV1.PlacementPayload, error) {
	var payload locoControllerV1.PlacementPayload
	if err := json.Unmarshal(placement.DesiredSpec, &payload); err != nil {
		return payload, fmt.Errorf("decode placement %s: %w", placement.ID, err)
	}
	if payload.AppSpec == nil {
		return payload, fmt.Errorf("placement %s: %w", placement.ID, errPlacementApplicationMissing)
	}
	return payload, nil
}

// rewritePlacement stores a changed payload as the placement's next desired revision.
func rewritePlacement(
	ctx context.Context,
	qtx *genDb.Queries,
	placement genDb.Placement,
	payload locoControllerV1.PlacementPayload,
) error {
	desired, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode placement %s: %w", placement.ID, err)
	}
	return placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
		ResourceID:    placement.ResourceID,
		ClusterID:     placement.ClusterID,
		Region:        placement.Region,
		DeploymentID:  placement.DeploymentID,
		DesiredSpec:   desired,
		EnvironmentID: placement.EnvironmentID,
		SecretNames:   placement.SecretNames,
	})
}
