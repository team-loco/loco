package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

// placementLiveStatuses are the placement statuses a region can still serve or roll out in.
var placementLiveStatuses = []genDb.PlacementStatus{
	genDb.PlacementStatusPending,
	genDb.PlacementStatusDeploying,
	genDb.PlacementStatusRunning,
	genDb.PlacementStatusDegraded,
	genDb.PlacementStatusCrashed,
}

func nextPlacementStatus(previous genDb.PlacementStatus, report *agentv1.PlacementStatus) genDb.PlacementStatus {
	if report.GetAvailable() {
		return genDb.PlacementStatusRunning
	}
	if report.GetProgressDeadlineExceeded() {
		return genDb.PlacementStatusFailed
	}
	return previous
}

func placementStatusNames(statuses ...genDb.PlacementStatus) []string {
	names := make([]string, 0, len(statuses))
	for _, status := range statuses {
		names = append(names, string(status))
	}
	return names
}

// recomputeDeploymentStatus locks a deployment, then stores the aggregate of its placements as
// committed when the lock is granted.
func recomputeDeploymentStatus(
	ctx context.Context,
	qtx *genDb.Queries,
	id uuid.UUID,
) (genDb.RecomputeDeploymentStatusRow, error) {
	if err := qtx.LockDeployment(ctx, id); err != nil {
		return genDb.RecomputeDeploymentStatusRow{}, fmt.Errorf("lock deployment %s: %w", id, err)
	}
	status, err := qtx.RecomputeDeploymentStatus(ctx, id)
	if err != nil {
		return genDb.RecomputeDeploymentStatusRow{}, fmt.Errorf("recompute deployment %s: %w", id, err)
	}
	return status, nil
}

// liveDeployment is the deployment one region of a resource's environment runs or is about to
// run, with that region's placement.
type liveDeployment struct {
	genDb.Deployment
	ClusterID       uuid.UUID
	Region          string
	Replicas        int32
	PlacementStatus genDb.PlacementStatus
}

// liveEnv is the plain env a region's live deployment runs with: the placement's while it is
// placed, the deployment's own while it waits for its build.
func liveEnv(ctx context.Context, q genDb.Querier, live liveDeployment) (map[string]string, error) {
	if live.PlacementStatus != genDb.PlacementStatusPending {
		return desiredEnv(ctx, q, live.ResourceID, live.EnvironmentID, live.ClusterID)
	}
	var stored struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(live.Spec, &stored); err != nil {
		return nil, fmt.Errorf("decode building deployment %s env: %w", live.ID, err)
	}
	return stored.Env, nil
}

func liveDeployments(rows []genDb.ListLiveDeploymentsForResourceRow) []liveDeployment {
	live := make([]liveDeployment, 0, len(rows))
	for _, row := range rows {
		live = append(live, liveDeployment{
			Deployment:      row.Deployment,
			ClusterID:       row.ClusterID,
			Region:          row.Region,
			Replicas:        row.Replicas,
			PlacementStatus: row.PlacementStatus,
		})
	}
	return live
}

func listLiveDeploymentsForResource(
	ctx context.Context,
	q genDb.Querier,
	resourceID uuid.UUID,
) ([]liveDeployment, error) {
	rows, err := q.ListLiveDeploymentsForResource(ctx, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list live deployments: %w", err)
	}
	return liveDeployments(rows), nil
}

func listLiveDeploymentsForEnvironment(
	ctx context.Context,
	q genDb.Querier,
	resourceID, environmentID uuid.UUID,
) ([]liveDeployment, error) {
	all, err := listLiveDeploymentsForResource(ctx, q, resourceID)
	if err != nil {
		return nil, err
	}
	var live []liveDeployment
	for _, deployment := range all {
		if deployment.EnvironmentID == environmentID {
			live = append(live, deployment)
		}
	}
	return live, nil
}
