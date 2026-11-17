package service

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errRolloutInProgress = rpcerr.NewCoded(
	connect.CodeFailedPrecondition,
	"a deployment is rolling out; wait for it to finish before changing this service",
)

func ensureNoRollout(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	region string,
	allowed ...genDb.DeploymentStatus,
) error {
	deployments, err := qtx.ListActiveDeploymentsForResource(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("read rollout state: %w", err)
	}
	for _, deployment := range deployments {
		if deployment.EnvironmentID != environmentID || (region != "" && deployment.Region != region) {
			continue
		}
		if !slices.Contains(allowed, deployment.Status) &&
			(deployment.Status == genDb.DeploymentStatusBuilding || deployment.Status == genDb.DeploymentStatusDeploying) {
			return errRolloutInProgress
		}
	}
	return nil
}
