package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
)

var errResourceEnvironmentNotFound = rpcerr.NewCoded(connect.CodeFailedPrecondition,
	"resource is not configured in this environment")

func resourceEnvironment(
	ctx context.Context,
	q genDb.Querier,
	resourceID, environmentID uuid.UUID,
) (genDb.ResourceEnvironment, error) {
	config, err := q.GetResourceEnvironment(ctx, genDb.GetResourceEnvironmentParams{
		ResourceID: resourceID, EnvironmentID: environmentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.ResourceEnvironment{}, errResourceEnvironmentNotFound
	}
	if err != nil {
		return genDb.ResourceEnvironment{}, fmt.Errorf("get resource environment: %w", err)
	}
	return config, nil
}

// A first deployment starts from its explicit targets, never another environment's defaults.
func (s *DeploymentServer) deploymentEnvironment(
	ctx context.Context,
	q *genDb.Queries,
	resource genDb.Resource,
	env genDb.Environment,
	service *deploymentv1.ServiceDeploymentSpec,
	regions []string,
) (genDb.ResourceEnvironment, error) {
	config, err := q.GetResourceEnvironment(
		ctx,
		genDb.GetResourceEnvironmentParams{ResourceID: resource.ID, EnvironmentID: env.ID},
	)
	if err == nil {
		return config, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return genDb.ResourceEnvironment{}, fmt.Errorf("get deployment environment: %w", err)
	}
	if len(regions) == 0 {
		return genDb.ResourceEnvironment{}, rpcerr.NewCoded(
			connect.CodeInvalidArgument,
			"choose regions for the first deployment in this environment",
		)
	}
	targets := make(map[string]*resourcev1.RegionTarget, len(regions))
	for _, region := range regions {
		size := s.defaults.Fill(nil, service.GetRegions()[region])
		targets[region] = &resourcev1.RegionTarget{Enabled: true, Cpu: size.CPU, Memory: size.Memory,
			MinReplicas: size.MinReplicas, MaxReplicas: size.MaxReplicas, Scalers: size.Scalers}
	}
	spec := &resourcev1.ServiceSpec{Regions: targets, Routing: &resourcev1.RoutingConfig{Port: service.Port,
		PathPrefix: service.GetRouting().GetPathPrefix(), IdleTimeout: service.GetRouting().GetIdleTimeout()},
		HealthCheck: service.GetHealthCheck(), Observability: service.GetObservability()}
	data, err := protojson.Marshal(spec)
	if err != nil {
		return genDb.ResourceEnvironment{}, fmt.Errorf("marshal initial environment spec: %w", err)
	}
	return q.UpsertResourceEnvironment(ctx, genDb.UpsertResourceEnvironmentParams{ResourceID: resource.ID,
		EnvironmentID: env.ID, WorkspaceID: resource.WorkspaceID, Spec: data, SpecVersion: specVersion})
}

func freezeDeploymentDefaults(
	service *deploymentv1.ServiceDeploymentSpec,
	config genDb.ResourceEnvironment,
	defaults servicedefaults.Defaults,
) error {
	service.FailurePolicy = defaults.FailurePolicy(config.RestartPolicy, config.MaxRestarts, config.DeployTimeoutSeconds).
		Proto()
	if _, err := deploymentFailurePolicy(service.GetFailurePolicy()); err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, err)
	}
	path, timeout := defaults.Routing(service.GetRouting().GetPathPrefix(), service.GetRouting().GetIdleTimeout())
	service.Routing = &deploymentv1.ServiceRouting{PathPrefix: path, IdleTimeout: timeout}
	return nil
}

func deploymentFailurePolicy(policy *deploymentv1.FailurePolicy) (servicedefaults.FailurePolicy, error) {
	filled := servicedefaults.FailurePolicy{
		Restart: locoControllerV1.RestartPolicySpec{
			Mode:        servicedefaults.RestartMode(policy.GetRestartPolicy()),
			MaxRestarts: policy.GetMaxRestarts(),
		},
		DeployTimeoutSeconds: policy.GetDeployTimeoutSeconds(),
	}
	if err := filled.Validate(); err != nil {
		return servicedefaults.FailurePolicy{}, err
	}
	return filled, nil
}

func resourceEnvironmentsToProto(
	configs []genDb.ResourceEnvironment,
	resourceType genDb.ResourceType,
	defaults servicedefaults.Defaults,
) ([]*resourcev1.ResourceEnvironment, error) {
	result := make([]*resourcev1.ResourceEnvironment, 0, len(configs))
	for _, config := range configs {
		spec, err := converter.DeserializeResourceSpec(config.Spec, resourceType)
		if err != nil {
			return nil, fmt.Errorf("decode resource environment spec: %w", err)
		}
		result = append(result, &resourcev1.ResourceEnvironment{
			EnvironmentId: config.EnvironmentID.String(),
			Spec:          spec,
			SpecVersion:   config.SpecVersion,
			Revision:      config.Revision,
			FailurePolicy: defaults.FailurePolicy(config.RestartPolicy, config.MaxRestarts, config.DeployTimeoutSeconds).
				Proto(),
		})
	}
	return result, nil
}
