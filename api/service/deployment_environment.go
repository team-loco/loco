package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/varref"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// resolveVariables resolves the variables of a deployment of resource to the environment. The
// caller must be able to read every other resource the variables refer to directly, and to list
// the environment's secrets when the variables read one.
func (s *DeploymentServer) resolveVariables(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	environmentID uuid.UUID,
	hostname string,
	service *deploymentv1.ServiceDeploymentSpec,
) (varref.Resolved, error) {
	references, err := loadVariableReferences(ctx, qtx, resource.WorkspaceID, environmentID)
	if err != nil {
		return varref.Resolved{}, err
	}
	resolved, err := references.resolve(resource.Slug, hostname, service, s.maxSecretBytes)
	if err != nil {
		return varref.Resolved{}, err
	}
	if len(resolved.SecretNames()) > 0 {
		if authErr := authorize(ctx, s.authz, actions.ListSecrets, resource.WorkspaceID); authErr != nil {
			return varref.Resolved{}, authErr
		}
	}
	for _, name := range resolved.Resources {
		if authErr := authorize(ctx, s.authz, actions.GetResource, references.ids[name]); authErr != nil {
			return varref.Resolved{}, authErr
		}
	}
	return resolved, nil
}
