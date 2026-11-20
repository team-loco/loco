package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

// rollbackDeploymentIfEnabled creates a deployment that restores, in every region the failed
// deployment replaced, the newest earlier deployment that ran, when the service asks for
// rollbacks. A failed rollback does not roll back again.
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
	if failed.Trigger == genDb.DeploymentTriggerAutoRollback {
		return nil
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
	targets, err := rollbackTargets(ctx, qtx, failed, service)
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
			ActorType:          failed.ActorType,
			ActorID:            failed.ActorID,
			Method:             failed.Method,
			CommitSha:          previous.CommitSha,
			CommitDirty:        previous.CommitDirty,
			Trigger:            genDb.DeploymentTriggerAutoRollback,
			SourceDeploymentID: &previous.ID,
		},
		service:  service,
		resolved: storedResolution(previous),
		targets:  targets,
		render:   render.payload,
	})
	if err != nil {
		return err
	}
	if err = events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentCreated,
		ActorType:     events.ActorSystem,
		EnvironmentID: new(failed.EnvironmentID),
		WorkspaceID:   new(resource.WorkspaceID),
		ResourceID:    new(resource.ID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(restored),
		Data: map[string]any{
			events.FieldResourceID:    resource.ID.String(),
			events.FieldEnvironmentID: failed.EnvironmentID.String(),
			events.FieldRegions:       targetRegions(targets),
		},
	}); err != nil {
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

// rollbackTargets are the regions whose live placement runs the failed deployment and that the
// previous spec sizes.
func rollbackTargets(
	ctx context.Context,
	qtx *genDb.Queries,
	failed genDb.Deployment,
	previous *deploymentv1.ServiceDeploymentSpec,
) ([]placementTarget, error) {
	placements, err := qtx.ListDeploymentPlacements(ctx, failed.ID)
	if err != nil {
		return nil, fmt.Errorf("list failed placements: %w", err)
	}
	var targets []placementTarget
	for _, placement := range placements {
		if _, sized := previous.GetRegions()[placement.Region]; !sized {
			continue
		}
		live, err := qtx.GetResourceEnvironmentPlacement(ctx, genDb.GetResourceEnvironmentPlacementParams{
			ResourceID: failed.ResourceID, EnvironmentID: failed.EnvironmentID, ClusterID: placement.ClusterID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read failed placement: %w", err)
		}
		if !live.DesiredDeleted && live.DeploymentID != nil && *live.DeploymentID == failed.ID {
			targets = append(targets, placementTarget{region: placement.Region, clusterID: placement.ClusterID})
		}
	}
	return targets, nil
}

func shortDeploymentID(id uuid.UUID) string {
	return id.String()[28:]
}
