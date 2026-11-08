package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func createDeploymentWithCleanup(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.CreateDeploymentParams,
	buildSpec desiredSpecFunc,
) (uuid.UUID, error) {
	if err := ensureOrganizationActive(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	if err := lockResource(ctx, qtx, params.ResourceID); err != nil {
		return uuid.UUID{}, err
	}
	if assignErr := lockAssignableCluster(ctx, qtx, params.ClusterID); assignErr != nil {
		return uuid.UUID{}, assignErr
	}
	if heldErr := checkClusterEnvironment(ctx, qtx, params); heldErr != nil {
		return uuid.UUID{}, heldErr
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
		newStatus := finalizedDeploymentStatus(activeDeployment.Status)
		slog.InfoContext(ctx, "finalizing previous deployment",
			"deploymentId", activeDeployment.ID,
			"oldStatus", activeDeployment.Status,
			"newStatus", newStatus)

		if updateErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       activeDeployment.ID,
			Status:   newStatus,
			IsActive: false,
		}); updateErr != nil {
			return uuid.UUID{}, fmt.Errorf("failed to finalize deployment %v: %w", activeDeployment.ID, updateErr)
		}
	}

	deploymentID, err := qtx.CreateDeployment(ctx, params)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to create deployment: %w", err)
	}

	spec, err := buildSpec(deploymentID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: %w", errDesiredSpec, err)
	}

	if hadPreviousDeployment && activeDeployment.ClusterID != params.ClusterID {
		if removeErr := removePlacement(ctx, qtx, params.ResourceID, activeDeployment.ClusterID); removeErr != nil {
			return uuid.UUID{}, removeErr
		}
	}

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

	slog.InfoContext(ctx, "deployment created",
		"deployment_id", deploymentID,
		"cluster_id", params.ClusterID,
		"resourceId", params.ResourceID,
		"region", params.Region,
		"hadPreviousDeployment", hadPreviousDeployment)

	return deploymentID, nil
}

// checkClusterEnvironment refuses a deployment to a cluster whose placement of the resource
// belongs to another environment. Loco runs one dedicated cluster per environment, and the
// cluster names the service's Application after the resource alone, so a second environment
// on the same cluster would overwrite the first one's workload.
func checkClusterEnvironment(ctx context.Context, qtx *genDb.Queries, params genDb.CreateDeploymentParams) error {
	placement, err := qtx.GetPlacementForResourceCluster(ctx, genDb.GetPlacementForResourceClusterParams{
		ResourceID: params.ResourceID,
		ClusterID:  params.ClusterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get placement: %w", err)
	}
	if placement.DesiredDeleted || placement.EnvironmentID == params.EnvironmentID {
		return nil
	}
	cluster, err := qtx.GetClusterByID(ctx, params.ClusterID)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	other, err := qtx.GetEnvironmentByID(ctx, placement.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}
	return fmt.Errorf(
		"%w: cluster %s already runs it for environment %s",
		errClusterHeldByEnvironment,
		cluster.Name,
		other.Name,
	)
}

// retireDeployment takes a deployment out of service: it drops the deployment's placement,
// finalizes its status and records the deletion.
func retireDeployment(ctx context.Context, qtx *genDb.Queries, res genDb.Resource, deployment genDb.Deployment) error {
	// an inactive deployment was superseded, and its cluster's placement now belongs to the
	// replacement.
	if deployment.IsActive {
		if err := removePlacement(ctx, qtx, res.ID, deployment.ClusterID); err != nil {
			return err
		}
	}
	err := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
		ID:       deployment.ID,
		Status:   finalizedDeploymentStatus(deployment.Status),
		IsActive: false,
	})
	if err != nil {
		return fmt.Errorf("finalize deployment %s: %w", deployment.ID, err)
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.DeploymentDeleted,
		WorkspaceID: new(res.WorkspaceID),
		ResourceID:  new(res.ID),
		SubjectType: events.SubjectDeployment,
		SubjectID:   new(deployment.ID),
		Data:        map[string]any{events.FieldResourceID: res.ID.String()},
	})
}

func finalizedDeploymentStatus(status genDb.DeploymentStatus) genDb.DeploymentStatus {
	switch status {
	case genDb.DeploymentStatusPending:
		return genDb.DeploymentStatusCanceled
	case genDb.DeploymentStatusDeploying:
		return genDb.DeploymentStatusCanceled
	case genDb.DeploymentStatusRunning:
		return genDb.DeploymentStatusSucceeded
	default:
		return status
	}
}

func deploymentTxError(ctx context.Context, err error) error {
	if errors.Is(err, errClusterHeldByEnvironment) {
		slog.WarnContext(ctx, "refused a deployment to another environment's cluster", "error", err)
		return rpcerr.New(connect.CodeFailedPrecondition, err)
	}
	if invalidSpec, ok := errors.AsType[*invalidSpecError](err); ok {
		slog.WarnContext(ctx, "rejected an invalid deployment spec", "error", err)
		return rpcerr.New(connect.CodeInvalidArgument, invalidSpec)
	}
	if errors.Is(err, errDesiredSpec) {
		slog.ErrorContext(ctx, "failed to build desired application spec", "error", err)
		return connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}
	if isUniqueViolation(err, pgActiveDeploymentUnique) {
		slog.WarnContext(ctx, "concurrent deployment rejected", "error", err)
		return connect.NewError(
			connect.CodeAborted,
			"another deployment for this resource and region is in progress",
		)
	}
	return handlerError(ctx, "failed to create deployment", err)
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
		obs := converter.ProtoToObsSpec(resourceService.GetObservability())
		if metrics := deploymentService.GetMetrics(); metrics != nil {
			if obs == nil && metrics.GetEnabled() {
				obs = &locoControllerV1.ObsSpec{}
			}
			if obs != nil {
				obs.Metrics = locoControllerV1.MetricsSpec{
					Enabled: metrics.GetEnabled(),
					Port:    metrics.GetPort(),
					Path:    metrics.GetPath(),
				}
			}
		}
		if obs != nil && obs.Metrics.Enabled {
			obs.Metrics.Path = servicedefaults.FirstSet(obs.Metrics.Path, defaults.MetricsPath)
		}
		appSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  converter.ProtoToResourcesSpec(deploymentService),
			Obs:        obs,
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

func (e *invalidSpecError) Unwrap() error {
	return e.err
}
