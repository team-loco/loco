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

// deploymentBindings returns the plain env and the secret names a deployment to regions runs
// with: what the request sets, else what the deployment live in the first of those regions
// runs with.
func (s *DeploymentServer) deploymentBindings(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	regions []string,
	bindings *deploymentv1.DeploymentSecretBindings,
	requestedEnv map[string]string,
) (map[string]string, []string, error) {
	secretNames := []string{}
	if bindings != nil {
		secretNames = append(secretNames, bindings.GetNames()...)
		if err := s.validateSecretBindings(ctx, qtx, resourceID, environmentID, secretNames); err != nil {
			return nil, nil, err
		}
	}
	live, err := listLiveDeploymentsForEnvironment(ctx, qtx, resourceID, environmentID)
	if err != nil {
		return nil, nil, err
	}
	index := slices.IndexFunc(live, func(d liveDeployment) bool { return slices.Contains(regions, d.Region) })
	if index < 0 {
		return requestedEnv, secretNames, nil
	}
	current := live[index]
	if bindings == nil {
		secretNames = current.SecretNames
	}
	if len(requestedEnv) > 0 {
		return requestedEnv, secretNames, nil
	}
	plain, err := liveEnv(ctx, qtx, current)
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
		held[size.Name] = size.CiphertextBytes - secretkeys.Overhead
	}
	if errs := configplan.CheckSecretBindings(names, held, int64(s.maxSecretBytes)); len(errs) > 0 {
		return errs[0]
	}
	return nil
}
