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
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

// rollbackDeploymentIfEnabled creates a deployment that restores, in every region of the failed
// deployment, the newest earlier deployment that ran, when the service asks for rollbacks.
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
	service := previousSpec.GetService()
	targets, err := rollbackTargets(ctx, qtx, failed.ID, service)
	if err != nil || len(targets) == 0 {
		return err
	}
	targetErr := lockPinnedBuildImage(ctx, qtx, service.GetBuild())
	for _, target := range targets {
		if targetErr == nil {
			targetErr = lockAssignableCluster(ctx, qtx, target.clusterID, false, service.GetPlatform())
		}
	}
	_, unavailable := errors.AsType[*rpcerr.CodedError](targetErr)
	if unavailable || errors.Is(targetErr, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "skipping rollback to unavailable deployment",
			"deployment_id", failed.ID,
			"previous_deployment_id", previous.ID,
			"error", targetErr,
		)
		return nil
	}
	if targetErr != nil {
		return targetErr
	}
	service.Env, err = desiredEnv(ctx, qtx, failed.ResourceID, failed.EnvironmentID, targets[0].clusterID)
	if err != nil {
		return err
	}
	regions := make(map[string]*deploymentv1.RegionDeploymentTarget, len(targets))
	for _, target := range targets {
		regions[target.region] = service.GetRegions()[target.region]
	}
	service.Regions = regions
	render, err := newPlacementRenderer(ctx, qtx, resource, failed.EnvironmentID, defaults)
	if err != nil {
		return err
	}
	restored, err := createDeployment(ctx, qtx, nil, newDeployment{
		params: genDb.CreateDeploymentParams{
			ResourceID:         failed.ResourceID,
			EnvironmentID:      failed.EnvironmentID,
			SecretNames:        previous.SecretNames,
			ActorType:          failed.ActorType,
			ActorID:            failed.ActorID,
			Method:             failed.Method,
			CommitSha:          previous.CommitSha,
			CommitDirty:        previous.CommitDirty,
			Trigger:            genDb.DeploymentTriggerAutoRollback,
			SourceDeploymentID: &previous.ID,
		},
		service: service,
		targets: targets,
		render:  render,
	})
	if err != nil {
		return err
	}
	if err = recordDeploymentCreated(ctx, qtx, resource, failed.EnvironmentID, restored, targetRegions(targets)); err != nil {
		return err
	}
	if err = writeDeploymentActivity(ctx, qtx, failed.ID, activityScope{}, []activityEntry{
		{Message: "Rolled back to " + shortDeploymentID(previous.ID), Severity: activityWarn},
	}); err != nil {
		return err
	}
	return writeDeploymentActivity(ctx, qtx, restored, activityScope{}, []activityEntry{
		{Message: "Restored after " + shortDeploymentID(failed.ID) + " failed", Severity: activityInfo},
	})
}

// rollbackTargets are the regions of the failed deployment the previous spec sizes, on the
// clusters the failed deployment used.
func rollbackTargets(
	ctx context.Context,
	qtx *genDb.Queries,
	failedID uuid.UUID,
	previous *deploymentv1.ServiceDeploymentSpec,
) ([]placementTarget, error) {
	placements, err := qtx.ListDeploymentPlacements(ctx, failedID)
	if err != nil {
		return nil, fmt.Errorf("list failed placements: %w", err)
	}
	var targets []placementTarget
	for _, placement := range placements {
		if placement.Status == genDb.PlacementStatusCanceled {
			continue
		}
		if _, sized := previous.GetRegions()[placement.Region]; sized {
			targets = append(targets, placementTarget{region: placement.Region, clusterID: placement.ClusterID})
		}
	}
	return targets, nil
}

func shortDeploymentID(id uuid.UUID) string {
	return id.String()[28:]
}
