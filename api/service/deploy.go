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
	"google.golang.org/protobuf/encoding/protojson"

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

// createDeploymentWithCleanup records the deployment, prepares placement and prunes history.
func createDeploymentWithCleanup(
	ctx context.Context,
	qtx *genDb.Queries,
	counts limits.Effective,
	params genDb.CreateDeploymentParams,
	buildSpec desiredSpecFunc,
) (uuid.UUID, error) {
	if err := ensureOrganizationActive(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	if err := lockResource(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	if assignErr := lockAssignableCluster(ctx, qtx, params.ClusterID, false); assignErr != nil {
		return uuid.UUID{}, assignErr
	}

	activeDeployment, err := qtx.GetActiveDeploymentForResourceAndRegion(
		ctx,
		genDb.GetActiveDeploymentForResourceAndRegionParams{
			ResourceID:    params.ResourceID,
			EnvironmentID: params.EnvironmentID,
			Region:        params.Region,
		},
	)
	hadPreviousDeployment := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("failed to get active deployment: %w", err)
	}

	if hadPreviousDeployment {
		newStatus := activeDeployment.Status
		if activeDeployment.StartedAt == nil {
			newStatus = genDb.DeploymentStatusCanceled
		}

		if updateErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       activeDeployment.ID,
			Status:   newStatus,
			IsActive: false,
		}); updateErr != nil {
			return uuid.UUID{}, fmt.Errorf("failed to finalize deployment %v: %w", activeDeployment.ID, updateErr)
		}
	}

	var pendingBuild genDb.Build
	var service deploymentv1.ServiceDeploymentSpec
	if decodeErr := protojson.Unmarshal(params.Spec, &service); decodeErr != nil {
		return uuid.Nil, fmt.Errorf("decode deployment spec: %w", decodeErr)
	}
	if rawBuildID := service.GetBuild().GetBuildId(); rawBuildID != "" {
		buildID := uuid.MustParse(rawBuildID)
		build, getErr := qtx.GetBuildByID(ctx, buildID)
		if getErr != nil {
			return uuid.Nil, getErr
		}
		pendingBuild = build
		if !buildStatusFinished(build.Status) {
			params.Status = genDb.DeploymentStatusBuilding
		}
	}
	deploymentID, err := qtx.CreateDeployment(ctx, params)
	if isUniqueViolation(err, pgActiveDeploymentUnique) {
		return uuid.UUID{}, errDeploymentInProgress
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to create deployment: %w", err)
	}

	spec, err := buildSpec(deploymentID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: %w", errDesiredSpec, err)
	}

	if params.Status == genDb.DeploymentStatusBuilding {
		if envErr := qtx.SetBuildingDeploymentEnv(ctx, genDb.SetBuildingDeploymentEnvParams{
			ID: deploymentID, DesiredSpec: spec,
		}); envErr != nil {
			return uuid.Nil, fmt.Errorf("store building deployment environment: %w", envErr)
		}
		if activityErr := writeDeploymentActivity(
			ctx,
			qtx,
			deploymentID,
			0,
			buildActivity(genDb.Build{}, pendingBuild, time.Time{}),
		); activityErr != nil {
			return uuid.Nil, activityErr
		}
	} else {
		err = placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
			ResourceID:    params.ResourceID,
			ClusterID:     params.ClusterID,
			Region:        params.Region,
			DeploymentID:  &deploymentID,
			DesiredSpec:   spec,
			EnvironmentID: params.EnvironmentID,
			SecretNames:   params.SecretNames,
		})
		if err != nil {
			return uuid.UUID{}, err
		}
	}

	if pruneErr := pruneDeploymentHistory(ctx, qtx, counts, params); pruneErr != nil {
		return uuid.UUID{}, pruneErr
	}

	slog.InfoContext(ctx, "deployment created",
		"deployment_id", deploymentID,
		"cluster_id", params.ClusterID,
		"resourceId", params.ResourceID,
		"region", params.Region,
		"hadPreviousDeployment", hadPreviousDeployment)

	return deploymentID, nil
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

func retireDeployment(ctx context.Context, qtx *genDb.Queries, res genDb.Resource, deployment genDb.Deployment) error {
	// Only the active deployment owns the placement.
	if deployment.IsActive {
		if err := removePlacement(ctx, qtx, res.ID, deployment.EnvironmentID, deployment.ClusterID); err != nil {
			return err
		}
	}
	status := deployment.Status
	switch status {
	case genDb.DeploymentStatusBuilding, genDb.DeploymentStatusDeploying, genDb.DeploymentStatusRunning:
		status = genDb.DeploymentStatusCanceled
	}
	err := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID:       deployment.ID,
		Status:   status,
		IsActive: false,
	})
	if err != nil {
		return fmt.Errorf("finalize deployment %s: %w", deployment.ID, err)
	}
	return events.Record(ctx, qtx, events.Event{
		Type:          events.DeploymentDeleted,
		EnvironmentID: new(deployment.EnvironmentID),
		WorkspaceID:   new(res.WorkspaceID),
		ResourceID:    new(res.ID),
		SubjectType:   events.SubjectDeployment,
		SubjectID:     new(deployment.ID),
		Data:          map[string]any{events.FieldResourceID: res.ID.String()},
	})
}

func desiredApplicationSpec(
	res genDb.Resource,
	organizationID uuid.UUID,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	region string,
	environmentID uuid.UUID,
	environmentName string,
	defaults servicedefaults.Defaults,
) desiredSpecFunc {
	return func(deploymentID uuid.UUID) ([]byte, error) {
		appSpec, err := buildApplicationSpec(
			res,
			organizationID,
			resourceSpec,
			hostname,
			deploymentSpec,
			region,
			environmentID,
			environmentName,
			deploymentID,
			defaults,
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
}

var errDesiredSpec = errors.New("build desired spec")

type desiredSpecFunc func(deploymentID uuid.UUID) ([]byte, error)

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

// buildApplicationSpec builds the ApplicationSpec for the loco controller.
// This is used both for direct k8s calls and for agent command payloads.
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
	// convert proto to controller CRD types
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(deploymentSpec)

	appSpec := &locoControllerV1.ApplicationSpec{
		ResourceID:      resource.ID.String(),
		Slug:            resource.Slug,
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
		appSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  converter.ProtoToResourcesSpec(deploymentService),
			Routing: converter.ProtoToRoutingSpec(
				deploymentRouting(resourceService, deploymentService),
				hostname,
				defaults,
			),
		}

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resource.Type)
	}

	// validate the ApplicationSpec before returning
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

func placeDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	resource genDb.Resource,
	deployment genDb.Deployment,
	spec *deploymentv1.DeploymentSpec,
	defaults servicedefaults.Defaults,
) error {
	resourceSpec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		return err
	}
	owner, err := qtx.GetWorkspaceOrganizationIDByResourceID(ctx, resource.ID)
	if err != nil {
		return err
	}
	env, err := qtx.GetEnvironmentByID(ctx, deployment.EnvironmentID)
	if err != nil {
		return err
	}
	hostname, err := primaryHostname(ctx, qtx, resource.ID, env.ID)
	if err != nil {
		return err
	}
	desired := desiredApplicationSpec(
		resource,
		owner.OrgID,
		resourceSpec,
		hostname,
		spec,
		deployment.Region,
		env.ID,
		env.Name,
		defaults,
	)
	payload, err := desired(deployment.ID)
	if err != nil {
		return err
	}
	return placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
		ResourceID: resource.ID, EnvironmentID: env.ID, Region: deployment.Region, ClusterID: deployment.ClusterID,
		DeploymentID: &deployment.ID, SecretNames: deployment.SecretNames, DesiredSpec: payload,
	})
}
