package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// placementTarget is one region a deployment rolls out to and the cluster that runs it there.
type placementTarget struct {
	region    string
	clusterID uuid.UUID
}

// newDeployment is one deployment to record: its row, its merged service spec with the plain
// env, and the regions it targets.
type newDeployment struct {
	params  genDb.CreateDeploymentParams
	service *deploymentv1.ServiceDeploymentSpec
	targets []placementTarget
	render  func(spec *deploymentv1.DeploymentSpec, deploymentID uuid.UUID, region string) ([]byte, error)
}

// createDeployment records one deployment with a placement per target region, places each
// region (or waits for the build), cancels older placements that never ran there, and prunes
// history when counts are given.
func createDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	counts *limits.Effective,
	d newDeployment,
) (uuid.UUID, error) {
	params := d.params
	if err := ensureOrganizationActive(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	if err := lockResource(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	for _, target := range d.targets {
		if err := lockAssignableCluster(ctx, qtx, target.clusterID, false, d.service.GetPlatform()); err != nil {
			return uuid.UUID{}, err
		}
	}

	params.Status = genDb.DeploymentStatusDeploying
	placementStatus := genDb.PlacementStatusDeploying
	var pendingBuild genDb.Build
	if rawBuildID := d.service.GetBuild().GetBuildId(); rawBuildID != "" {
		build, err := qtx.GetBuildByID(ctx, uuid.MustParse(rawBuildID))
		if err != nil {
			return uuid.Nil, err
		}
		pendingBuild = build
		if !buildStatusFinished(build.Status) {
			params.Status = genDb.DeploymentStatusBuilding
			placementStatus = genDb.PlacementStatusPending
		}
	}
	specJSON, err := deploymentSpecJSON(d.service)
	if err != nil {
		return uuid.UUID{}, err
	}
	params.Spec = specJSON
	params.SpecVersion = specVersion
	deploymentID, err := qtx.CreateDeployment(ctx, params)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to create deployment: %w", err)
	}

	spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: d.service}}
	image := ""
	if placementStatus == genDb.PlacementStatusDeploying {
		image = d.service.GetBuild().GetImage()
	}
	for _, target := range d.targets {
		if createErr := qtx.CreateDeploymentPlacement(ctx, genDb.CreateDeploymentPlacementParams{
			DeploymentID: deploymentID,
			ClusterID:    target.clusterID,
			Region:       target.region,
			Replicas:     d.service.GetRegions()[target.region].GetMinReplicas(),
			Status:       placementStatus,
			Image:        image,
		}); createErr != nil {
			return uuid.UUID{}, fmt.Errorf("create deployment placement: %w", createErr)
		}
		if placementStatus == genDb.PlacementStatusDeploying {
			payload, renderErr := d.render(spec, deploymentID, target.region)
			if renderErr != nil {
				return uuid.UUID{}, fmt.Errorf("%w: %w", errDesiredSpec, renderErr)
			}
			if placeErr := placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
				ResourceID:    params.ResourceID,
				ClusterID:     target.clusterID,
				Region:        target.region,
				DeploymentID:  &deploymentID,
				DesiredSpec:   payload,
				EnvironmentID: params.EnvironmentID,
				SecretNames:   params.SecretNames,
			}); placeErr != nil {
				return uuid.UUID{}, placeErr
			}
		}
		if supersedeErr := supersedePlacements(ctx, qtx, deploymentID, target.clusterID, false); supersedeErr != nil {
			return uuid.UUID{}, supersedeErr
		}
	}

	if placementStatus == genDb.PlacementStatusPending {
		env, marshalErr := json.Marshal(d.service.GetEnv())
		if marshalErr != nil {
			return uuid.Nil, fmt.Errorf("encode building deployment environment: %w", marshalErr)
		}
		if envErr := qtx.SetBuildingDeploymentEnv(ctx, genDb.SetBuildingDeploymentEnvParams{
			ID: deploymentID, Env: env,
		}); envErr != nil {
			return uuid.Nil, fmt.Errorf("store building deployment environment: %w", envErr)
		}
		if activityErr := writeDeploymentActivity(
			ctx,
			qtx,
			deploymentID,
			activityScope{},
			buildActivity(genDb.Build{}, pendingBuild, time.Time{}),
		); activityErr != nil {
			return uuid.Nil, activityErr
		}
	}

	if counts != nil {
		if pruneErr := pruneDeploymentHistory(ctx, qtx, *counts, params); pruneErr != nil {
			return uuid.UUID{}, pruneErr
		}
	}

	slog.InfoContext(ctx, "deployment created",
		"deployment_id", deploymentID,
		"resourceId", params.ResourceID,
		"regions", targetRegions(d.targets))

	return deploymentID, nil
}

// placementTargets picks the cluster each region deploys to from eligibleClusters' listing.
func placementTargets(
	clusters []genDb.ListEligibleClustersRow,
	regions []string,
	platform string,
) ([]placementTarget, error) {
	targets := make([]placementTarget, 0, len(regions))
	for _, region := range regions {
		cluster, found := clusterForRegion(clusters, region)
		if !found {
			return nil, errNoActiveCluster.Detail("%s, requires %s", region, platform)
		}
		targets = append(targets, placementTarget{region: region, clusterID: cluster.ID})
	}
	return targets, nil
}

func targetRegions(targets []placementTarget) []string {
	regions := make([]string, 0, len(targets))
	for _, target := range targets {
		regions = append(regions, target.region)
	}
	return regions
}

// supersedePlacements retires the older placements on the cluster that the deployment replaces
// and recomputes their deployments' status.
func supersedePlacements(
	ctx context.Context,
	qtx *genDb.Queries,
	deploymentID, clusterID uuid.UUID,
	includeStarted bool,
) error {
	retired, err := qtx.SupersedePlacements(ctx, genDb.SupersedePlacementsParams{
		DeploymentID: deploymentID, ClusterID: clusterID, IncludeStarted: includeStarted,
	})
	if err != nil {
		return fmt.Errorf("supersede placements: %w", err)
	}
	for _, id := range retired {
		status, recomputeErr := recomputeDeploymentStatus(ctx, qtx, id)
		if recomputeErr != nil {
			return fmt.Errorf("recompute superseded deployment %s: %w", id, recomputeErr)
		}
		if status.Status != genDb.DeploymentStatusCanceled || status.PreviousStatus == status.Status {
			continue
		}
		if canceledErr := recordSupersededCancel(ctx, qtx, id, deploymentID); canceledErr != nil {
			return canceledErr
		}
	}
	return nil
}

// recordSupersededCancel cancels the build a superseded deployment waited for, unless another
// deployment uses it, and records the deployment's cancellation.
func recordSupersededCancel(ctx context.Context, qtx *genDb.Queries, id, supersededBy uuid.UUID) error {
	canceled, err := qtx.GetDeploymentByID(ctx, id)
	if err != nil {
		return fmt.Errorf("read superseded deployment %s: %w", id, err)
	}
	if cancelErr := cancelUnusedBuild(ctx, qtx, canceled); cancelErr != nil {
		return cancelErr
	}
	return events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentCanceled,
		ActorType:     events.ActorSystem,
		ResourceID:    new(canceled.ResourceID),
		EnvironmentID: new(canceled.EnvironmentID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(id),
		Data: map[string]any{
			events.FieldDeploymentID: id.String(),
			events.FieldSupersededBy: supersededBy.String(),
		},
	})
}

func pruneDeploymentHistory(
	ctx context.Context,
	qtx *genDb.Queries,
	counts limits.Effective,
	params genDb.CreateDeploymentParams,
) error {
	keep, err := limits.DeploymentHistory.Get(counts)
	if err != nil {
		return err
	}
	pruned, err := qtx.PruneDeploymentHistory(ctx, genDb.PruneDeploymentHistoryParams{
		ResourceID:    params.ResourceID,
		EnvironmentID: params.EnvironmentID,
		Keep:          keep,
	})
	if err != nil {
		return fmt.Errorf("prune deployment history: %w", err)
	}
	if pruned > 0 {
		slog.InfoContext(ctx, "pruned deployment history", "resourceId", params.ResourceID, "deleted", pruned)
	}
	return nil
}

// retireDeployment takes one region of a deployment out of service: its placement is removed
// and canceled. A deployment left with no region records deployment.deleted.
func retireDeployment(ctx context.Context, qtx *genDb.Queries, res genDb.Resource, live liveDeployment) error {
	if err := removePlacement(ctx, qtx, res.ID, live.EnvironmentID, live.ClusterID); err != nil {
		return err
	}
	if _, err := qtx.SetDeploymentPlacementStatus(ctx, genDb.SetDeploymentPlacementStatusParams{
		Status:       genDb.PlacementStatusCanceled,
		DeploymentID: live.ID,
		ClusterID:    live.ClusterID,
		FromStatuses: placementStatusNames(placementLiveStatuses...),
	}); err != nil {
		return fmt.Errorf("cancel placement of deployment %s: %w", live.ID, err)
	}
	status, err := recomputeDeploymentStatus(ctx, qtx, live.ID)
	if err != nil {
		return fmt.Errorf("recompute retired deployment %s: %w", live.ID, err)
	}
	if status.Status != genDb.DeploymentStatusCanceled || status.PreviousStatus == status.Status {
		return nil
	}
	return events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentDeleted,
		EnvironmentID: new(live.EnvironmentID),
		WorkspaceID:   new(res.WorkspaceID),
		ResourceID:    new(res.ID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(live.ID),
		Data:          map[string]any{events.FieldResourceID: res.ID.String()},
	})
}

// placementRenderer renders the Application a deployment's placement carries in one region.
type placementRenderer struct {
	resource        genDb.Resource
	organizationID  uuid.UUID
	resourceSpec    *resourcev1.ResourceSpec
	hostname        string
	environmentID   uuid.UUID
	environmentName string
	defaults        servicedefaults.Defaults
}

// newPlacementRenderer reads what rendering a resource's placements in an environment needs.
func newPlacementRenderer(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	environmentID uuid.UUID,
	defaults servicedefaults.Defaults,
) (placementRenderer, error) {
	resourceSpec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		return placementRenderer{}, fmt.Errorf("%w: decode resource spec: %w", errDesiredSpec, err)
	}
	owner, err := qtx.GetWorkspaceOrganizationIDByResourceID(ctx, resource.ID)
	if err != nil {
		return placementRenderer{}, err
	}
	env, err := qtx.GetEnvironmentByID(ctx, environmentID)
	if err != nil {
		return placementRenderer{}, err
	}
	hostname, err := primaryHostname(ctx, qtx, resource.ID, env.ID)
	if err != nil {
		return placementRenderer{}, err
	}
	return placementRenderer{
		resource:        resource,
		organizationID:  owner.OrgID,
		resourceSpec:    resourceSpec,
		hostname:        hostname,
		environmentID:   env.ID,
		environmentName: env.Name,
		defaults:        defaults,
	}, nil
}

func (r placementRenderer) payload(
	spec *deploymentv1.DeploymentSpec,
	deploymentID uuid.UUID,
	region string,
) ([]byte, error) {
	appSpec, err := buildApplicationSpec(
		r.resource,
		r.organizationID,
		r.resourceSpec,
		r.hostname,
		spec,
		region,
		r.environmentID,
		r.environmentName,
		deploymentID,
		r.defaults,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build application spec: %w", err)
	}
	payload, err := json.Marshal(locoControllerV1.PlacementPayload{AppSpec: appSpec})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application payload: %w", err)
	}
	return payload, nil
}

var errDesiredSpec = errors.New("build desired spec")

func primaryHostname(
	ctx context.Context,
	queries genDb.Querier,
	resourceID uuid.UUID,
	environmentID uuid.UUID,
) (string, error) {
	params := genDb.GetPrimaryResourceDomainParams{ResourceID: resourceID, EnvironmentID: environmentID}
	hostname, err := queries.GetPrimaryResourceDomain(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get primary domain: %w", err)
	}
	return hostname, nil
}

// buildApplicationSpec builds the ApplicationSpec a placement carries to the controller.
func buildApplicationSpec(
	resource genDb.Resource,
	organizationID uuid.UUID,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	region string,
	environmentID uuid.UUID,
	environmentName string,
	deploymentID uuid.UUID,
	defaults servicedefaults.Defaults,
) (*locoControllerV1.ApplicationSpec, error) {
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(deploymentSpec)

	appSpec := &locoControllerV1.ApplicationSpec{
		ResourceID:      resource.ID.String(),
		Slug:            resource.Name,
		OrganizationID:  organizationID.String(),
		WorkspaceID:     resource.WorkspaceID.String(),
		Region:          region,
		EnvironmentID:   environmentID.String(),
		EnvironmentName: environmentName,
		DeploymentID:    deploymentID.String(),
	}

	switch resource.Type {
	case genDb.ResourceTypeService:
		appSpec.Type = "SERVICE"
		resourceService := resourceSpec.GetService()
		if _, ok := resourceService.GetRegions()[region]; !ok {
			return nil, &invalidSpecError{err: fmt.Errorf("target region %s not found in service spec", region)}
		}
		deploymentService := deploymentSpec.GetService()
		target, ok := deploymentService.GetRegions()[region]
		if !ok {
			return nil, &invalidSpecError{err: fmt.Errorf("target region %s not found in deployment spec", region)}
		}
		appSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  converter.ProtoToResourcesSpec(target),
			Routing: converter.ProtoToRoutingSpec(
				deploymentRouting(resourceService, deploymentService),
				hostname,
				defaults,
			),
			RestartPolicy: defaults.RestartPolicy(resource.MaxRestarts, resource.RestartWindowSeconds),
		}

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resource.Type)
	}

	if err := appSpec.Validate(); err != nil {
		return nil, &invalidSpecError{err: err}
	}

	return appSpec, nil
}

type invalidSpecError struct {
	err error
}

func (e *invalidSpecError) Error() string {
	return "invalid deployment spec: " + e.err.Error()
}

// Unwrap puts the public message first, so rpcerr.Public reports it rather than a coded error
// inside the cause.
func (e *invalidSpecError) Unwrap() []error {
	return []error{rpcerr.NewCoded(connect.CodeInvalidArgument, e.Error()), e.err}
}

// placeDeployment points the region's placement at an existing deployment, with the given
// spec, which carries the plain env.
func placeDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	render placementRenderer,
	deployment genDb.Deployment,
	target placementTarget,
	spec *deploymentv1.DeploymentSpec,
) error {
	payload, err := render.payload(spec, deployment.ID, target.region)
	if err != nil {
		return err
	}
	return placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
		ResourceID: deployment.ResourceID, EnvironmentID: deployment.EnvironmentID, Region: target.region,
		ClusterID: target.clusterID, DeploymentID: &deployment.ID, SecretNames: deployment.SecretNames,
		DesiredSpec: payload,
	})
}
