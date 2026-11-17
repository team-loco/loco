package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
)

func rollbackDeploymentIfEnabled(
	ctx context.Context,
	qtx *genDb.Queries,
	id uuid.UUID,
	defaults servicedefaults.Defaults,
) error {
	failed, err := qtx.GetDeploymentByID(ctx, id)
	if err != nil {
		return fmt.Errorf("read failed deployment: %w", err)
	}
	if !failed.IsActive {
		return nil
	}
	placement, err := qtx.PlacementForDeployment(ctx, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read failed placement: %w", err)
	}
	resource, err := qtx.GetResourceByID(ctx, failed.ResourceID)
	if err != nil {
		return err
	}
	spec, err := converter.DeserializeDeploymentSpec(failed.Spec, resource.Type)
	if err != nil {
		return err
	}
	resourceSpec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		return err
	}
	enabled := resourceSpec.GetService().GetRollbackOnFailure()
	if override := spec.GetService().RollbackOnFailure; override != nil {
		enabled = *override
	}
	if !enabled {
		return nil
	}
	previous, err := qtx.PreviousDeploymentForRollback(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find previous deployment: %w", err)
	}
	targetErr := lockAssignableCluster(ctx, qtx, previous.ClusterID, false)
	if targetErr == nil {
		targetErr = checkClusterEnvironment(ctx, qtx, genDb.CreateDeploymentParams{
			ResourceID: previous.ResourceID, ClusterID: previous.ClusterID, EnvironmentID: previous.EnvironmentID,
		})
	}
	if targetErr == nil {
		previousSpec, decodeErr := converter.DeserializeDeploymentSpec(previous.Spec, resource.Type)
		if decodeErr != nil {
			return decodeErr
		}
		targetErr = lockPinnedBuildImage(ctx, qtx, previousSpec.GetService().GetBuild())
	}
	if targetErr != nil {
		for _, unavailable := range []error{
			ErrClusterNotAssignable, errClusterApplicationsNotAllowed, errClusterHeldByEnvironment,
			errBuildImageDeleted, pgx.ErrNoRows,
		} {
			if !errors.Is(targetErr, unavailable) {
				continue
			}

			slog.WarnContext(
				ctx,
				"skipping rollback to unavailable deployment",
				"deployment_id",
				failed.ID,
				"previous_deployment_id",
				previous.ID,
				"cluster_id",
				previous.ClusterID,
				"error",
				targetErr,
			)
			return nil
		}
		return targetErr
	}
	plainEnv, envErr := desiredEnv(ctx, qtx, failed.ResourceID, failed.ClusterID)
	if envErr != nil {
		return envErr
	}
	if opErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID: failed.ID, Status: genDb.DeploymentStatusFailed, IsActive: false,
	}); opErr != nil {
		return opErr
	}
	if opErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID: previous.ID, Status: genDb.DeploymentStatusDeploying, IsActive: true,
	}); opErr != nil {
		return opErr
	}
	if opErr := placeDeployment(ctx, qtx, previous, "", plainEnv, defaults); opErr != nil {
		return opErr
	}
	if opErr := writeDeploymentActivity(ctx, qtx, failed.ID, placement.DesiredRevision, []activityEntry{
		{Message: "Rolled back to " + previous.ID.String()[:8], Severity: activityWarn},
	}); opErr != nil {
		return opErr
	}
	restored, err := qtx.PlacementForDeployment(ctx, &previous.ID)
	if err != nil {
		return err
	}
	return writeDeploymentActivity(ctx, qtx, previous.ID, restored.DesiredRevision, []activityEntry{
		{Message: "Restored after " + failed.ID.String()[:8] + " failed", Severity: activityInfo},
	})
}
