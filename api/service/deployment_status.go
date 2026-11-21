package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// placementLiveStatuses are the placement statuses a region can still serve or roll out in.
var placementLiveStatuses = []genDb.PlacementStatus{
	genDb.PlacementStatusPending,
	genDb.PlacementStatusDeploying,
	genDb.PlacementStatusRunning,
	genDb.PlacementStatusDegraded,
	genDb.PlacementStatusCrashed,
}

// nextPlacementStatus is where a report of the placement's current revision moves it. A stopped
// revision fails when the previous revision serves in its place or it missed its deploy timeout,
// and crashes otherwise. A deployed revision whose replica used up its restarts keeps running and
// crashes until the replica recovers; one whose replica restarted or stopped being ready is
// degraded until the controller reports it healthy again.
func nextPlacementStatus(previous genDb.PlacementStatus, report *agentv1.PlacementStatus) genDb.PlacementStatus {
	ran := previous == genDb.PlacementStatusRunning || previous == genDb.PlacementStatusDegraded
	switch {
	// A deploy-timeout stop is the only stop that counts no restarts.
	case report.GetStopped().GetRollout(), report.GetStopped() != nil && report.GetStopped().GetRestarts() == 0:
		return genDb.PlacementStatusFailed
	case report.GetStopped() != nil,
		report.GetProblem().GetReason() == string(loco.ProblemRestartsExhausted):
		return genDb.PlacementStatusCrashed
	case report.GetHealthy():
		return genDb.PlacementStatusRunning
	case ran:
		return genDb.PlacementStatusDegraded
	default:
		return previous
	}
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
