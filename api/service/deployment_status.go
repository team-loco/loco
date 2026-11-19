package service

import (
	"context"
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
	if report.GetProgressDeadlineExceeded() && previous == genDb.PlacementStatusDeploying {
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
