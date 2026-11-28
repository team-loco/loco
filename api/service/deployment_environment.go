package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/varref"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

// resolveVariables resolves the variables of a deployment of resource to the environment. The
// caller must be able to read every other resource the variables refer to directly, and to list
// the environment's secrets when the actual bindings read one. Source replays retain their
// captured bindings and check access to the original direct references. The checks run on qtx.
func (s *DeploymentServer) resolveVariables(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	environmentID uuid.UUID,
	hostname string,
	service *deploymentv1.ServiceDeploymentSpec,
	source *genDb.Deployment,
) (varref.Resolved, error) {
	references, err := loadVariableReferences(ctx, qtx, resource.WorkspaceID, environmentID)
	if err != nil {
		return varref.Resolved{}, err
	}
	var resolved varref.Resolved
	if source == nil {
		resolved, err = references.environment.Resolve(
			resource.Slug, varref.Target{Port: service.GetPort(), PublicHost: hostname}, service.GetEnv(),
		)
		if err != nil {
			return varref.Resolved{}, err
		}
	} else {
		resolved = storedResolution(*source)
		resolved.Resources = varref.ReferencedResources(service.GetEnv())
	}
	if err = references.validateSecrets(resolved.Secret, s.maxSecretBytes); err != nil {
		return varref.Resolved{}, err
	}
	authorizer := authz.New(qtx)
	if len(resolved.Secret) > 0 {
		if authErr := authorize(ctx, authorizer, actions.ListSecrets, resource.WorkspaceID); authErr != nil {
			return varref.Resolved{}, authErr
		}
	}
	for _, name := range resolved.Resources {
		if name == resource.Slug {
			continue
		}
		id, exists := references.ids[name]
		if !exists {
			return varref.Resolved{}, varref.ErrUnknownResource.Detail("%s", name)
		}
		if authErr := authorize(ctx, authorizer, actions.GetResource, id); authErr != nil {
			return varref.Resolved{}, authErr
		}
	}
	return resolved, nil
}
