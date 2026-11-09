package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func (s *DeploymentServer) deploymentBindings(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	req *deploymentv1.CreateDeploymentRequest,
	requestedEnv map[string]string,
) (map[string]string, []string, error) {
	secretNames := []string{}
	bindings := req.GetSecretBindings()
	if bindings != nil {
		secretNames = append(secretNames, bindings.GetNames()...)
		if err := s.validateSecretBindings(ctx, qtx, resourceID, environmentID, secretNames); err != nil {
			return nil, nil, err
		}
	}
	params := genDb.GetActiveDeploymentForResourceAndRegionParams{
		ResourceID:    resourceID,
		EnvironmentID: environmentID,
		Region:        req.GetRegion(),
	}
	current, err := qtx.GetActiveDeploymentForResourceAndRegion(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return requestedEnv, secretNames, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get current deployment environment: %w", err)
	}
	if bindings == nil {
		secretNames = current.SecretNames
	}
	if len(requestedEnv) > 0 {
		return requestedEnv, secretNames, nil
	}
	plain, err := desiredEnv(ctx, qtx, resourceID, current.ClusterID)
	if err != nil {
		return nil, nil, err
	}
	return plain, secretNames, nil
}

func (s *DeploymentServer) validateSecretBindings(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	names []string,
) error {
	if len(names) == 0 {
		return nil
	}
	sizes, err := qtx.ListSecretSizes(ctx, genDb.ListSecretSizesParams{
		ResourceID: resourceID, EnvironmentID: environmentID, Names: names,
	})
	if err != nil {
		return fmt.Errorf("list deployment secret sizes: %w", err)
	}
	held := make(map[string]int64, len(sizes))
	for _, size := range sizes {
		held[size.Name] = size.CiphertextBytes - secretTagBytes
	}
	if errs := configplan.CheckSecretBindings(names, held, int64(s.maxSecretBytes)); len(errs) > 0 {
		return errs[0]
	}
	return nil
}
