package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/varref"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/rpcerr"

	"connectrpc.com/connect/v2"
)

var ErrSecretEnvTooLarge = rpcerr.NewCoded(
	connect.CodeFailedPrecondition,
	"the secret values one placement of the service reads exceed the size limit",
)

// environmentTarget is one resource of the workspace as references in an environment see it.
type environmentTarget struct {
	id        uuid.UUID
	slug      string
	target    varref.Target
	secretEnv map[string]string
}

// listEnvironmentTargets reads every resource of the workspace with its latest deployment in the
// environment.
func listEnvironmentTargets(
	ctx context.Context,
	q genDb.Querier,
	workspaceID uuid.UUID,
	environmentID uuid.UUID,
) ([]environmentTarget, error) {
	rows, err := q.ListEnvironmentTargets(ctx, genDb.ListEnvironmentTargetsParams{
		WorkspaceID: workspaceID, EnvironmentID: environmentID,
	})
	if err != nil {
		return nil, fmt.Errorf("list environment targets: %w", err)
	}
	targets := make([]environmentTarget, 0, len(rows))
	for _, row := range rows {
		target := varref.Target{}
		if row.PrimaryDomain != nil {
			target.PublicHost = *row.PrimaryDomain
		}
		if row.DeploymentSpec != nil {
			spec, specErr := converter.DeserializeDeploymentSpec(row.DeploymentSpec, genDb.ResourceTypeService)
			if specErr != nil {
				return nil, fmt.Errorf("decode the latest deployment of %s: %w", row.Slug, specErr)
			}
			target.Port = spec.GetService().GetPort()
			target.Env = spec.GetService().GetEnv()
		}
		targets = append(targets, environmentTarget{
			id: row.ID, slug: row.Slug, target: target, secretEnv: row.SecretEnv,
		})
	}
	return targets, nil
}

// secretSizes holds the plaintext size of each secret of an environment by name.
type secretSizes map[string]int64

func listSecretSizes(ctx context.Context, q genDb.Querier, environmentID uuid.UUID) (secretSizes, error) {
	rows, err := q.ListSecretSizes(ctx, environmentID)
	if err != nil {
		return nil, fmt.Errorf("list secret sizes: %w", err)
	}
	sizes := make(secretSizes, len(rows))
	for _, row := range rows {
		sizes[row.Name] = row.CiphertextBytes - secretkeys.Overhead
	}
	return sizes, nil
}

func (s secretSizes) names() map[string]bool {
	names := make(map[string]bool, len(s))
	for name := range s {
		names[name] = true
	}
	return names
}

// variableReferences is what the variables of a resource deployed to an environment resolve
// against.
type variableReferences struct {
	environment varref.Environment
	sizes       secretSizes
	ids         map[string]uuid.UUID
}

func loadVariableReferences(
	ctx context.Context,
	q genDb.Querier,
	workspaceID uuid.UUID,
	environmentID uuid.UUID,
) (variableReferences, error) {
	targets, err := listEnvironmentTargets(ctx, q, workspaceID, environmentID)
	if err != nil {
		return variableReferences{}, err
	}
	sizes, err := listSecretSizes(ctx, q, environmentID)
	if err != nil {
		return variableReferences{}, err
	}
	resources := make(map[string]varref.Target, len(targets))
	ids := make(map[string]uuid.UUID, len(targets))
	for _, target := range targets {
		resources[target.slug] = target.target
		ids[target.slug] = target.id
	}
	return variableReferences{
		environment: varref.Environment{Secrets: sizes.names(), Resources: resources},
		sizes:       sizes,
		ids:         ids,
	}, nil
}

// resolve resolves the variables of service, deployed as the resource with slug name with the given
// primary domain, and checks the secret values its placements carry fit maxSecretBytes.
func (v variableReferences) resolve(
	name string,
	hostname string,
	service *deploymentv1.ServiceDeploymentSpec,
	maxSecretBytes int32,
) (varref.Resolved, error) {
	target := varref.Target{Port: service.GetPort(), PublicHost: hostname}
	resolved, err := v.environment.Resolve(name, target, service.GetEnv())
	if err != nil {
		return varref.Resolved{}, err
	}
	if total := varref.SecretBytes(resolved.Secret, v.sizes); total > int64(maxSecretBytes) {
		return varref.Resolved{}, ErrSecretEnvTooLarge.Detail("%d bytes over a limit of %d", total, maxSecretBytes)
	}
	return resolved, nil
}

// withPlainEnv returns a copy of spec that runs with env as its variables.
func withPlainEnv(spec *deploymentv1.DeploymentSpec, env map[string]string) *deploymentv1.DeploymentSpec {
	clone := proto.CloneOf(spec)
	if service := clone.GetService(); service != nil {
		service.Env = env
	}
	return clone
}
