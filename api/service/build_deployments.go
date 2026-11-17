package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
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
		status := deployment.Status
		switch build.Status {
		case genDb.BuildStatusSucceeded:
			if err := placeDeployment(ctx, qtx, deployment, buildImage(build), nil, defaults); err != nil {
				return err
			}
			status = genDb.DeploymentStatusDeploying
		case genDb.BuildStatusFailed:
			if err := storeBuildProblem(ctx, qtx, deployment.ID, build); err != nil {
				return err
			}
			status = genDb.DeploymentStatusFailed
		case genDb.BuildStatusCanceled:
			status = genDb.DeploymentStatusCanceled
		}
		if status != deployment.Status {
			if err := advanceDeploymentStatus(ctx, qtx, genDb.AdvanceDeploymentStatusParams{
				ID: deployment.ID, Status: status, FromStatuses: []string{string(genDb.DeploymentStatusBuilding)},
			}, build.Message, defaults); err != nil {
				return err
			}
		}
		if err := writeDeploymentActivity(ctx, qtx, deployment.ID, 0, entries); err != nil {
			return err
		}
	}
	return nil
}

func storeBuildProblem(ctx context.Context, qtx *genDb.Queries, deploymentID uuid.UUID, build genDb.Build) error {
	snapshot, err := json.Marshal(rolloutSnapshot{Problem: &agentv1.ProblemReport{
		Reason: string(loco.ProblemBuildFailed),
		Detail: build.Message,
		Since:  optionalTimestamp(build.FinishedAt),
	}})
	if err != nil {
		return fmt.Errorf("encode build problem: %w", err)
	}
	if err := qtx.OpenRollout(ctx, genDb.OpenRolloutParams{DeploymentID: deploymentID}); err != nil {
		return fmt.Errorf("open build rollout: %w", err)
	}
	return qtx.StoreRollout(ctx, genDb.StoreRolloutParams{DeploymentID: deploymentID, Snapshot: snapshot})
}
