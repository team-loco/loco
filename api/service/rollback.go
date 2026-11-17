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
	"github.com/team-loco/loco/internal/rpcerr"
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
	if !servicedefaults.RollbackOnFailure(
		resourceSpec.GetService().GetRollbackOnFailure(),
		spec.GetService().RollbackOnFailure,
	) {
		return nil
	}
	previous, err := qtx.PreviousDeploymentForRollback(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find previous deployment: %w", err)
	}
	previousSpec, err := converter.DeserializeDeploymentSpec(previous.Spec, resource.Type)
	if err != nil {
		return err
	}
	targetErr := lockAssignableCluster(ctx, qtx, previous.ClusterID, false)
	if targetErr == nil {
		targetErr = checkClusterEnvironment(ctx, qtx, genDb.CreateDeploymentParams{
			ResourceID: previous.ResourceID, ClusterID: previous.ClusterID, EnvironmentID: previous.EnvironmentID,
		})
	}
	if targetErr == nil {
		targetErr = lockPinnedBuildImage(ctx, qtx, previousSpec.GetService().GetBuild())
	}
	_, unavailable := errors.AsType[*rpcerr.CodedError](targetErr)
	if unavailable || errors.Is(targetErr, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "skipping rollback to unavailable deployment",
			"deployment_id", failed.ID,
			"previous_deployment_id", previous.ID,
			"cluster_id", previous.ClusterID,
			"error", targetErr,
		)
		return nil
	}
	if targetErr != nil {
		return targetErr
	}
	plainEnv, err := desiredEnv(ctx, qtx, failed.ResourceID, failed.ClusterID)
	if err != nil {
		return err
	}
	previousSpec.GetService().Env = plainEnv
	if err = qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID: failed.ID, Status: genDb.DeploymentStatusFailed, IsActive: false,
	}); err != nil {
		return err
	}
	if err = qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID: previous.ID, Status: genDb.DeploymentStatusDeploying, IsActive: true,
	}); err != nil {
		return err
	}
	if err = placeDeployment(ctx, qtx, resource, previous, previousSpec, defaults); err != nil {
		return err
	}
	if err = writeDeploymentActivity(ctx, qtx, failed.ID, placement.DesiredRevision, []activityEntry{
		{Message: "Rolled back to " + previous.ID.String()[28:], Severity: activityWarn},
	}); err != nil {
		return err
	}
	restored, err := qtx.PlacementForDeployment(ctx, &previous.ID)
	if err != nil {
		return err
	}
	return writeDeploymentActivity(ctx, qtx, previous.ID, restored.DesiredRevision, []activityEntry{
		{Message: "Restored after " + failed.ID.String()[28:] + " failed", Severity: activityInfo},
	})
}
