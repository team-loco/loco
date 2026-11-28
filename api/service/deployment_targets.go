package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// prepareDeploymentResource applies explicit edits to the defaults the deployment merges with.
// The caller holds the resource and every environment lock; replay has already returned.
func prepareDeploymentResource(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	req *deploymentv1.CreateDeploymentRequest,
	defaults *resourcev1.ServiceSpec,
	requested *deploymentv1.ServiceDeploymentSpec,
) error {
	if req.GetRemoveHealthCheck() {
		if removeErr := removeHealthCheck(ctx, qtx, resource.ID, defaults); removeErr != nil {
			return removeErr
		}
	}
	if !req.GetUpdateResourceTargets() {
		return nil
	}
	if resource.Partial != nil {
		return errPartialRegionEdit
	}
	if req.ResourceRevision == nil || resource.Revision != req.GetResourceRevision() {
		return errResourceRevisionMoved
	}
	defaults.Regions = resourceTargetsFromDeployment(requested.GetRegions())
	return nil
}

func resourceTargetsFromDeployment(
	targets map[string]*deploymentv1.RegionDeploymentTarget,
) map[string]*resourcev1.RegionTarget {
	result := make(map[string]*resourcev1.RegionTarget, len(targets))
	for name, target := range targets {
		result[name] = &resourcev1.RegionTarget{
			Enabled:     true,
			Cpu:         target.GetCpu(),
			Memory:      target.GetMemory(),
			MinReplicas: target.GetMinReplicas(),
			MaxReplicas: target.GetMaxReplicas(),
			Scalers:     target.GetScalers(),
		}
	}
	return result
}

// saveResourceTargets persists the validated merged targets without creating another rollout.
func saveResourceTargets(ctx context.Context, qtx *genDb.Queries, resource genDb.Resource,
	environmentID uuid.UUID, defaults *resourcev1.ServiceSpec, merged *deploymentv1.ServiceDeploymentSpec) error {
	defaults.Regions = resourceTargetsFromDeployment(merged.GetRegions())
	encoded, err := protojson.Marshal(defaults)
	if err != nil {
		return fmt.Errorf("marshal deployment resource targets: %w", err)
	}
	if updateErr := qtx.UpdateResourceSpec(
		ctx,
		genDb.UpdateResourceSpecParams{ID: resource.ID, Spec: encoded},
	); updateErr != nil {
		return fmt.Errorf("save deployment resource targets: %w", updateErr)
	}
	if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resource.ID); bumpErr != nil {
		return bumpErr
	}
	live, err := listLiveDeploymentsForEnvironment(ctx, qtx, resource.ID, environmentID)
	if err != nil {
		return err
	}
	for _, deployment := range live {
		if _, kept := defaults.GetRegions()[deployment.Region]; kept {
			continue
		}
		if err := retireDeployment(ctx, qtx, resource, deployment); err != nil {
			return err
		}
	}
	return events.Record(
		ctx,
		qtx,
		events.Event{
			Type:        events.ResourceUpdated,
			ResourceID:  new(resource.ID),
			SubjectType: events.SubjectResource,
			SubjectID:   new(resource.ID),
			Data:        map[string]any{events.FieldPaths: []string{"regions"}},
		},
	)
}
