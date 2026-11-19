package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// deploymentSecretNames returns the secret names a deployment to regions binds: what the
// request sets, else what the deployment live in the first of those regions binds.
func (s *DeploymentServer) deploymentSecretNames(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	regions []string,
	bindings *deploymentv1.DeploymentSecretBindings,
) ([]string, error) {
	if bindings != nil {
		secretNames := append([]string{}, bindings.GetNames()...)
		if err := s.validateSecretBindings(ctx, qtx, resourceID, environmentID, secretNames); err != nil {
			return nil, err
		}
		return secretNames, nil
	}
	live, err := listLiveDeploymentsForEnvironment(ctx, qtx, resourceID, environmentID)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(live, func(d liveDeployment) bool { return slices.Contains(regions, d.Region) })
	if index < 0 {
		return []string{}, nil
	}
	return live[index].SecretNames, nil
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
		held[size.Name] = size.CiphertextBytes - secretkeys.Overhead
	}
	if errs := configplan.CheckSecretBindings(names, held, int64(s.maxSecretBytes)); len(errs) > 0 {
		return errs[0]
	}
	return nil
}
