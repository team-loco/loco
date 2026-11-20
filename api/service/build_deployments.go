package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// FinishBuildDeployments records failed or canceled builds.
func FinishBuildDeployments(ctx context.Context, qtx *genDb.Queries, buildID uuid.UUID) error {
	build, err := qtx.GetBuildByID(ctx, buildID)
	if err != nil {
		return fmt.Errorf("read finished build: %w", err)
	}
	return updateBuildingDeployments(
		ctx,
		qtx,
		build,
		[]activityEntry{finishedBuildActivity(build)},
		servicedefaults.Defaults{},
	)
}

func buildActivity(previous, current genDb.Build, stageSince time.Time) []activityEntry {
	var entries []activityEntry
	if previous.Stage != current.Stage && current.Stage != "" {
		message := map[loco.BuildStage]string{
			loco.BuildStageFetch: "Fetching source",
			loco.BuildStageBuild: "Building image",
			loco.BuildStagePush:  "Pushing image",
		}[loco.BuildStage(current.Stage)]
		entries = append(entries, activityEntry{Message: message, Severity: activityInfo, At: stageSince})
	}
	if previous.Status != current.Status && buildStatusFinished(current.Status) {
		entries = append(entries, finishedBuildActivity(current))
	}
	return entries
}

func finishedBuildActivity(build genDb.Build) activityEntry {
	entry := activityEntry{}
	switch build.Status {
	case genDb.BuildStatusSucceeded:
		entry.Message, entry.Severity = "Build succeeded", activityOK
	case genDb.BuildStatusFailed:
		entry.Message, entry.Severity = "Build failed · "+build.Message, activityError
	case genDb.BuildStatusCanceled:
		entry.Message, entry.Severity = "Build canceled", activityWarn
	}
	if build.FinishedAt != nil {
		entry.At = *build.FinishedAt
	}
	return entry
}

// updateBuildingDeployments moves the deployments waiting for a finished build on: each pending
// placement is placed with the built image, or fails or is canceled with the build.
func updateBuildingDeployments(
	ctx context.Context,
	qtx *genDb.Queries,
	build genDb.Build,
	entries []activityEntry,
	defaults servicedefaults.Defaults,
) error {
	deployments, err := qtx.ListBuildingDeployments(ctx, build.ID.String())
	if err != nil {
		return fmt.Errorf("list building deployments: %w", err)
	}
	for _, deployment := range deployments {
		if err := writeDeploymentActivity(ctx, qtx, deployment.ID, activityScope{}, entries); err != nil {
			return err
		}
		if !buildStatusFinished(build.Status) {
			continue
		}
		placements, err := qtx.ListDeploymentPlacements(ctx, deployment.ID)
		if err != nil {
			return fmt.Errorf("list building placements: %w", err)
		}
		status := map[genDb.BuildStatus]genDb.PlacementStatus{
			genDb.BuildStatusSucceeded: genDb.PlacementStatusDeploying,
			genDb.BuildStatusFailed:    genDb.PlacementStatusFailed,
			genDb.BuildStatusCanceled:  genDb.PlacementStatusCanceled,
		}[build.Status]
		if build.Status == genDb.BuildStatusSucceeded {
			if err := placeBuiltDeployment(ctx, qtx, deployment, placements, build, defaults); err != nil {
				return err
			}
		}
		for _, placement := range placements {
			if placement.Status != genDb.PlacementStatusPending {
				continue
			}
			if build.Status == genDb.BuildStatusFailed {
				if err := storeBuildProblem(ctx, qtx, deployment.ID, placement.ClusterID, build); err != nil {
					return err
				}
			}
			if _, err := qtx.SetDeploymentPlacementStatus(ctx, genDb.SetDeploymentPlacementStatusParams{
				Status:       status,
				DeploymentID: deployment.ID,
				ClusterID:    placement.ClusterID,
				FromStatuses: placementStatusNames(genDb.PlacementStatusPending),
			}); err != nil {
				return fmt.Errorf("advance building placement: %w", err)
			}
		}
		cause := transitionCause{message: build.Message}
		if err := recordDeploymentStatus(ctx, qtx, deployment.ID, cause); err != nil {
			return err
		}
	}
	return nil
}

func storeBuildProblem(
	ctx context.Context,
	qtx *genDb.Queries,
	deploymentID, clusterID uuid.UUID,
	build genDb.Build,
) error {
	snapshot, err := json.Marshal(rolloutSnapshot{Problem: &agentv1.ProblemReport{
		Reason: string(loco.ProblemBuildFailed),
		Detail: build.Message,
		Since:  optionalTimestamp(build.FinishedAt),
	}})
	if err != nil {
		return fmt.Errorf("encode build problem: %w", err)
	}
	if err := qtx.OpenRollout(
		ctx,
		genDb.OpenRolloutParams{DeploymentID: deploymentID, ClusterID: clusterID},
	); err != nil {
		return fmt.Errorf("open build rollout: %w", err)
	}
	return qtx.StoreRollout(ctx, genDb.StoreRolloutParams{
		DeploymentID: deploymentID, ClusterID: clusterID, Snapshot: snapshot,
	})
}

func placeBuiltDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	deployment genDb.Deployment,
	placements []genDb.DeploymentPlacement,
	build genDb.Build,
	defaults servicedefaults.Defaults,
) error {
	resource, err := qtx.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		return fmt.Errorf("read build deployment resource: %w", err)
	}
	spec, err := converter.DeserializeDeploymentSpec(deployment.Spec, resource.Type)
	if err != nil {
		return err
	}
	render, err := newPlacementRenderer(ctx, qtx, resource, deployment.EnvironmentID, defaults)
	if err != nil {
		return err
	}
	image := buildImage(build)
	spec.GetService().GetBuild().Image = image
	for _, placement := range placements {
		if placement.Status != genDb.PlacementStatusPending {
			continue
		}
		target := placementTarget{region: placement.Region, clusterID: placement.ClusterID}
		if err := placeDeployment(ctx, qtx, render, deployment, target, spec); err != nil {
			return err
		}
		if err := supersedePlacements(ctx, qtx, deployment.ID, placement.ClusterID, false); err != nil {
			return err
		}
	}
	return qtx.SetDeploymentBuildImage(ctx, genDb.SetDeploymentBuildImageParams{ID: deployment.ID, Image: image})
}
