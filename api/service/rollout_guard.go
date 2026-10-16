package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errRolloutInProgress = errors.New("a deployment is rolling out; wait for it to finish before changing this service")

func ensureNoRollout(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	region string,
) error {
	deployments, err := qtx.ListActiveDeploymentsForResource(ctx, resourceID)
	if err != nil {
		return fmt.Errorf("read rollout state: %w", err)
	}
	for _, deployment := range deployments {
		if deployment.EnvironmentID != environmentID || (region != "" && deployment.Region != region) {
			continue
		}
		if deployment.Status == genDb.DeploymentStatusPending || deployment.Status == genDb.DeploymentStatusDeploying {
			return rpcerr.New(connect.CodeFailedPrecondition, errRolloutInProgress)
		}
	}
	return nil
}
