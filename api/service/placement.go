package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/internal/rpcerr"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var errManagedClusterMove = rpcerr.NewCoded(
	connect.CodeFailedPrecondition,
	"managed public routing requires an explicit cluster migration before moving a live placement within a region",
)

var errPlacementApplicationMissing = errors.New("placement application spec is missing")

func placeApplication(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.UpsertPlacementParams,
) error {
	managed, err := qtx.HasManagedPublicRoutes(
		ctx,
		genDb.HasManagedPublicRoutesParams{ResourceID: params.ResourceID, EnvironmentID: params.EnvironmentID},
	)
	if err != nil {
		return err
	}
	if managed {
		live, readErr := qtx.ListResourceEnvironmentPlacements(
			ctx,
			genDb.ListResourceEnvironmentPlacementsParams{
				ResourceID:    params.ResourceID,
				EnvironmentID: params.EnvironmentID,
			},
		)
		if readErr != nil {
			return readErr
		}
		for _, p := range live {
			if p.Region == params.Region && p.ClusterID != params.ClusterID {
				return errManagedClusterMove
			}
		}
	}
	if err = qtx.DeleteRoutingRetirement(
		ctx,
		genDb.DeleteRoutingRetirementParams{
			ResourceID:    params.ResourceID,
			EnvironmentID: params.EnvironmentID,
			ClusterID:     params.ClusterID,
		},
	); err != nil {
		return err
	}
	retired, err := qtx.RetireOtherRegionPlacements(ctx, genDb.RetireOtherRegionPlacementsParams{
		ResourceID:    params.ResourceID,
		EnvironmentID: params.EnvironmentID,
		Region:        params.Region,
		ClusterID:     params.ClusterID,
	})
	if err != nil {
		return fmt.Errorf("retire previous placements: %w", err)
	}
	removed := make([]genDb.MarkPlacementDeletedRow, 0, len(retired))
	for _, row := range retired {
		removed = append(removed, genDb.MarkPlacementDeletedRow(row))
	}
	if notifyErr := notifyRemovedPlacements(ctx, qtx, params.ResourceID, removed); notifyErr != nil {
		return notifyErr
	}
	placement, err := qtx.UpsertPlacement(ctx, params)
	if err != nil {
		return fmt.Errorf("upsert placement: %w", err)
	}
	window := genDb.OpenPlacementWindowParams{
		ResourceID: params.ResourceID,
		ClusterID:  params.ClusterID,
	}
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

func removePlacement(ctx context.Context, qtx *genDb.Queries, resourceID, environmentID, clusterID uuid.UUID) error {
	managed, err := qtx.HasManagedPublicRoutes(
		ctx,
		genDb.HasManagedPublicRoutesParams{ResourceID: resourceID, EnvironmentID: environmentID},
	)
	if err != nil {
		return err
	}
	if managed {
		return qtx.StageRoutingRetirement(
			ctx,
			genDb.StageRoutingRetirementParams{
				ResourceID:    resourceID,
				EnvironmentID: environmentID,
				ClusterID:     clusterID,
			},
		)
	}
	removed, err := qtx.MarkPlacementDeleted(ctx, genDb.MarkPlacementDeletedParams{
		ResourceID:    resourceID,
		EnvironmentID: environmentID,
		ClusterID:     clusterID,
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
		window := genDb.ClosePlacementWindowParams{
			ResourceID: resourceID,
			ClusterID:  placement.ClusterID,
		}
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

// renamePlacements writes the resource's slug into each of its live placements, so its clusters
// rename its private Service without a new deployment.
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

// restartedSpec is the placement's desired spec with its last restart time, which rolls its replicas.
func restartedSpec(placement genDb.Placement) ([]byte, error) {
	payload, err := decodePlacement(placement)
	if err != nil {
		return nil, err
	}
	payload.AppSpec.RestartedAt = placement.RestartedAt.UTC().Format(time.RFC3339Nano)
	spec, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode placement %s: %w", placement.ID, err)
	}
	return spec, nil
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
		SecretEnv:     placement.SecretEnv,
	})
}
