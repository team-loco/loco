package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func currentDeploymentBindings(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	region string,
	requestedEnv map[string]string,
) (map[string]string, []string, error) {
	params := genDb.GetActiveDeploymentForResourceAndRegionParams{
		ResourceID:    resourceID,
		EnvironmentID: environmentID,
		Region:        region,
	}
	current, err := qtx.GetActiveDeploymentForResourceAndRegion(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return requestedEnv, []string{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get current deployment environment: %w", err)
	}
	if len(requestedEnv) > 0 {
		return requestedEnv, current.SecretNames, nil
	}
	plain, err := desiredEnv(ctx, qtx, resourceID, current.ClusterID)
	if err != nil {
		return nil, nil, err
	}
	return plain, current.SecretNames, nil
}
