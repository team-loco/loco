package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	timeutil "github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errResourceSpecNotService = errors.New("resource spec missing service configuration")
	errDatabaseNotImplemented = errors.New("database resource type not yet implemented")
	errCacheNotImplemented    = errors.New("cache resource type not yet implemented")
	errQueueNotImplemented    = errors.New("queue resource type not yet implemented")
	errBlobNotImplemented     = errors.New("blob resource type not yet implemented")
	errServiceSpecRequired    = errors.New("service spec is required")
	ErrDeploymentNotFound     = errors.New("deployment not found")
)

type ApplicationPayload struct {
	DeploymentID string                            `json:"deployment_id"`
	ResourceID   string                            `json:"resource_id"`
	WorkspaceID  string                            `json:"workspace_id"`
	ResourceName string                            `json:"resource_name"`
	ResourceType string                            `json:"resource_type"`
	Region       string                            `json:"region"`
	Hostname     string                            `json:"hostname"`
	AppSpec      *locoControllerV1.ApplicationSpec `json:"app_spec"`
}

func parseDeploymentPhase(status genDb.DeploymentStatus) deploymentv1.DeploymentPhase {
	switch status {
	case genDb.DeploymentStatusPending:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING
	case genDb.DeploymentStatusDeploying:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING
	case genDb.DeploymentStatusRunning:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING
	case genDb.DeploymentStatusSucceeded:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED
	case genDb.DeploymentStatusFailed:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED
	case genDb.DeploymentStatusCanceled:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED
	default:
		return deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_UNSPECIFIED
	}
}

func deploymentToProto(d genDb.Deployment, resourceType string) *deploymentv1.Deployment {
	deployment := &deploymentv1.Deployment{
		Id:            d.ID.String(),
		ResourceId:    d.ResourceID.String(),
		EnvironmentId: d.EnvironmentID.String(),
		ClusterId:     d.ClusterID.String(),
		Region:        d.Region,
		Replicas:      d.Replicas,
		Status:        parseDeploymentPhase(d.Status),
		IsActive:      d.IsActive,
		CreatedAt:     timeutil.ParsePostgresTimestamp(d.CreatedAt),
		UpdatedAt:     timeutil.ParsePostgresTimestamp(d.UpdatedAt),
		SpecVersion:   d.SpecVersion,
		Message:       d.Message,
	}

	if len(d.Spec) > 0 {
		spec := &deploymentv1.DeploymentSpec{}

		switch resourceType {
		case string(genDb.ResourceTypeService):
			serviceSpec := &deploymentv1.ServiceDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, serviceSpec); err != nil {
				slog.WarnContext(
					context.Background(),
					"failed to unmarshal service deployment spec",
					"error",
					err,
					"deployment_id",
					d.ID,
				)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Service{Service: serviceSpec}
			}
		case string(genDb.ResourceTypeDatabase):
			databaseSpec := &deploymentv1.DatabaseDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, databaseSpec); err != nil {
				slog.WarnContext(
					context.Background(),
					"failed to unmarshal database deployment spec",
					"error",
					err,
					"deployment_id",
					d.ID,
				)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Database{Database: databaseSpec}
			}
		case string(genDb.ResourceTypeCache):
			cacheSpec := &deploymentv1.CacheDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, cacheSpec); err != nil {
				slog.WarnContext(
					context.Background(),
					"failed to unmarshal cache deployment spec",
					"error",
					err,
					"deployment_id",
					d.ID,
				)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Cache{Cache: cacheSpec}
			}
		case string(genDb.ResourceTypeQueue):
			queueSpec := &deploymentv1.QueueDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, queueSpec); err != nil {
				slog.WarnContext(
					context.Background(),
					"failed to unmarshal queue deployment spec",
					"error",
					err,
					"deployment_id",
					d.ID,
				)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Queue{Queue: queueSpec}
			}
		default:
			slog.WarnContext(
				context.Background(),
				"unknown resource type",
				"resource_type",
				resourceType,
				"deployment_id",
				d.ID,
			)
		}

		deployment.Spec = spec
	}

	deployment.StartedAt = timeutil.ParsePostgresTimestamp(d.StartedAt)
	if d.CompletedAt != nil {
		deployment.CompletedAt = timeutil.ParsePostgresTimestampPtr(d.CompletedAt)
	}

	return deployment
}

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db           *pgxpool.Pool
	queries      genDb.Querier
	machine      *tvm.VendingMachine
	resolver     ImageResolver
	registryHost string
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	resolver ImageResolver,
	registryHost string,
) *DeploymentServer {
	return &DeploymentServer{
		db:           db,
		queries:      queries,
		machine:      machine,
		resolver:     resolver,
		registryHost: registryHost,
	}
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.CreateDeploymentRequest],
) (*connect.Response[deploymentv1.CreateDeploymentResponse], error) {
	r := req.Msg

	resourceID := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.CreateDeployment, r.GetResourceId()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to create deployment", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	// validate that request spec contains a service deployment (for now, only services are supported)
	if r.GetSpec().GetService() == nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("only service deployments are currently supported"),
		)
	}

	serviceSpec := r.GetSpec().GetService()
	replicas := serviceSpec.GetMinReplicas()

	domain, err := s.queries.GetDomainByResourceId(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	region := r.GetRegion()
	environmentID := uuid.MustParse(r.GetEnvironmentId())

	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.WarnContext(ctx, "environment not found", "environmentId", environmentID)
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", environmentID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if env.WorkspaceID != resource.WorkspaceID {
		slog.WarnContext(
			ctx,
			"environment does not belong to the resource's workspace",
			"environmentId",
			environmentID,
			"resourceId",
			resourceID,
		)
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	// Get active cluster for the specified region and environment tier
	cluster, err := s.queries.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
		Region: region,
		Tier:   env.EnvironmentType,
	})
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to get active cluster for region",
			"region",
			region,
			"tier",
			env.EnvironmentType,
			"error",
			err,
		)
		return nil, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("no active cluster available for region %s tier %s", region, env.EnvironmentType),
		)
	}

	requestedBuild := serviceSpec.GetBuild()
	pinnedBuild, err := pinBuildSource(ctx, s.queries, s.resolver, s.registryHost, resourceID, requestedBuild)
	if err != nil {
		return nil, err
	}
	originalSpec := r.GetSpec()
	clonedSpec := proto.Clone(originalSpec)
	requestSpec, ok := clonedSpec.(*deploymentv1.DeploymentSpec)
	if !ok {
		slog.ErrorContext(ctx, "failed to clone deployment spec")
		return nil, connect.NewError(connect.CodeInternal, errCloneDeploymentSpec)
	}
	requestSpec.GetService().Build = pinnedBuild

	// deserialize resource spec and merge with request spec
	resourceSpec, deserializeErr := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	mergedSpec, mergeErr := converter.MergeDeploymentSpec(resourceSpec, requestSpec, region)
	if mergeErr != nil {
		slog.ErrorContext(ctx, mergeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("merge error: %w", mergeErr))
	}

	// create spec copy without env for DB persistence (no plaintext secrets in DB)
	mergedServiceSpec := mergedSpec.GetService()

	// todo: consider using dedicated secrets management solution.
	clonedServiceSpec := proto.Clone(mergedServiceSpec)
	specForDBService, ok := clonedServiceSpec.(*deploymentv1.ServiceDeploymentSpec)
	if !ok {
		slog.ErrorContext(ctx, "failed to clone service spec")
		return nil, connect.NewError(connect.CodeInternal, errCloneServiceSpec)
	}
	specForDBService.Env = nil

	specJSON, err := json.Marshal(specForDBService)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	// Get resource region for deployment record
	resourceRegion, err := s.queries.GetResourceRegionByResourceAndRegion(
		ctx,
		genDb.GetResourceRegionByResourceAndRegionParams{
			ResourceID: resourceID,
			Region:     region,
		},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource region", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("resource region not found"))
	}

	buildSpec := desiredApplicationSpec(
		resource,
		resourceSpec,
		domain.Domain,
		mergedSpec,
		region,
		environmentID,
		env.Name,
	)

	var deploymentID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var txErr error
		deploymentID, txErr = createDeploymentWithCleanup(ctx, qtx, genDb.CreateDeploymentParams{
			ResourceID:       resourceID,
			ResourceRegionID: resourceRegion.ID,
			ClusterID:        cluster.ID,
			Region:           region,
			Replicas:         replicas,
			Status:           genDb.DeploymentStatusPending,
			IsActive:         true,
			Message:          "Scheduling deployment",
			Spec:             specJSON,
			SpecVersion:      int32(1),
			EnvironmentID:    environmentID,
		}, buildSpec)
		return txErr
	})
	if err != nil {
		return nil, deploymentTxError(ctx, err)
	}

	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{DeploymentId: deploymentID.String()}), nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.GetDeploymentRequest],
) (*connect.Response[deploymentv1.GetDeploymentResponse], error) {
	r := req.Msg

	deploymentID := uuid.MustParse(r.GetDeploymentId())

	deploymentData, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.GetDeploymentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deploymentData.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	// check if user has permission to get deployment (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.GetDeployment, resource.ID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get deployment", "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&deploymentv1.GetDeploymentResponse{
		Deployment: deploymentToProto(deploymentData, string(resource.Type)),
	}), nil
}

// ListDeployments lists deployments for a resource
func (s *DeploymentServer) ListDeployments(
	ctx context.Context,
	req *connect.Request[deploymentv1.ListDeploymentsRequest],
) (*connect.Response[deploymentv1.ListDeploymentsResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	// check if requester has permission to list deployments (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.ListDeployments, r.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken *string
	if r.GetPageToken() != "" {
		cursorID, decodeErr := decodeCursor(r.GetPageToken())
		if decodeErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", decodeErr))
		}
		pageToken = &cursorID
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: resourceID,
		Limit:      pageSize,
		PageToken:  pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployments = append(deployments, deploymentToProto(d, string(resource.Type)))
	}

	var nextPageToken string
	if len(deploymentList) == int(pageSize) {
		nextPageToken = encodeCursor(deploymentList[len(deploymentList)-1].ID.String())
	}

	return connect.NewResponse(&deploymentv1.ListDeploymentsResponse{
		Deployments:   deployments,
		NextPageToken: nextPageToken,
	}), nil
}

// DeleteDeployment deletes/inactivates a deployment and cleans up its Application
func (s *DeploymentServer) DeleteDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.DeleteDeploymentRequest],
) (*connect.Response[deploymentv1.DeleteDeploymentResponse], error) {
	r := req.Msg

	deploymentID := uuid.MustParse(r.GetDeploymentId())

	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.GetDeploymentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.DeleteDeployment, resource.ID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to delete deployment", "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if deployment.IsActive {
			if removeErr := removePlacement(ctx, qtx, resource.ID, deployment.ClusterID); removeErr != nil {
				return removeErr
			}
		}

		if markErr := qtx.MarkDeploymentNotActive(ctx, deploymentID); markErr != nil {
			return fmt.Errorf("mark deployment not active: %w", markErr)
		}
		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&deploymentv1.DeleteDeploymentResponse{}), nil
}

const (
	watchDeploymentPollInterval = 2 * time.Second
	watchDeploymentMaxDuration  = 30 * time.Minute
)

// WatchDeployment streams deployment status updates
func (s *DeploymentServer) WatchDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.WatchDeploymentRequest],
	stream *connect.ServerStream[deploymentv1.WatchDeploymentResponse],
) error {
	r := req.Msg

	deploymentID := uuid.MustParse(r.GetDeploymentId())

	resourceID, err := s.queries.GetDeploymentResourceID(ctx, deploymentID)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.GetDeploymentId())
		return connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	resourceIDStr := resourceID.String()
	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.StreamDeployment, resourceIDStr),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to stream deployment", "resourceId", resourceIDStr)
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	ctx, cancel := context.WithTimeout(ctx, watchDeploymentMaxDuration)
	defer cancel()

	var lastStatus genDb.DeploymentStatus
	ticker := time.NewTicker(watchDeploymentPollInterval)
	defer ticker.Stop()

	for {
		status, err := s.sendDeploymentEvent(ctx, stream, deploymentID, lastStatus)
		if err != nil {
			return err
		}
		lastStatus = status
		if isTerminalDeploymentStatus(lastStatus) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminalDeploymentStatus(status genDb.DeploymentStatus) bool {
	switch status {
	case genDb.DeploymentStatusSucceeded:
		return true
	case genDb.DeploymentStatusFailed:
		return true
	case genDb.DeploymentStatusCanceled:
		return true
	default:
		return false
	}
}

func (s *DeploymentServer) sendDeploymentEvent(
	ctx context.Context,
	stream *connect.ServerStream[deploymentv1.WatchDeploymentResponse],
	deploymentID uuid.UUID,
	lastStatus genDb.DeploymentStatus,
) (genDb.DeploymentStatus, error) {
	deployment, err := s.queries.GetDeploymentStatus(ctx, deploymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get deployment status", "error", err)
		return lastStatus, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if deployment.Status == lastStatus {
		return lastStatus, nil
	}

	deploymentIDStr := deploymentID.String()
	statusPhase := parseDeploymentPhase(deployment.Status)
	now := time.Now()
	event := &deploymentv1.WatchDeploymentResponse{
		DeploymentId: deploymentIDStr,
		Status:       statusPhase,
		Message:      deployment.Message,
		Timestamp:    timestamppb.New(now),
	}

	if err := stream.Send(event); err != nil {
		return lastStatus, err
	}

	slog.InfoContext(ctx, "sent deployment event", "deployment_id", deploymentIDStr, "status", deployment.Status)
	return deployment.Status, nil
}

// buildApplicationSpec builds the ApplicationSpec for the loco controller.
// This is used both for direct k8s calls and for agent command payloads.
func buildApplicationSpec(
	resource genDb.Resource,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	region string,
	environmentID uuid.UUID,
	environmentName string,
	deploymentID uuid.UUID,
) (*locoControllerV1.ApplicationSpec, error) {
	// convert proto to controller CRD types
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(deploymentSpec)

	appSpec := &locoControllerV1.ApplicationSpec{
		ResourceID:      resource.ID.String(),
		WorkspaceID:     resource.WorkspaceID.String(),
		Region:          region,
		EnvironmentID:   environmentID.String(),
		EnvironmentName: environmentName,
		DeploymentID:    deploymentID.String(),
	}

	switch resource.Type {
	case genDb.ResourceTypeService:
		if resourceSpec.GetService() == nil {
			return nil, errResourceSpecNotService
		}
		appSpec.Type = "SERVICE"
		resourcesSpec, err := buildResourcesSpec(resourceSpec.GetService(), deploymentSpec, region)
		if err != nil {
			return nil, fmt.Errorf("failed to build resources spec: %w", err)
		}
		appSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  resourcesSpec,
			Obs:        converter.ProtoToObsSpec(resourceSpec.GetService().GetObservability()),
			Routing:    converter.ProtoToRoutingSpec(resourceSpec.GetService().GetRouting(), hostname),
		}

	case genDb.ResourceTypeDatabase:
		return nil, errDatabaseNotImplemented
	case genDb.ResourceTypeCache:
		return nil, errCacheNotImplemented
	case genDb.ResourceTypeQueue:
		return nil, errQueueNotImplemented
	case genDb.ResourceTypeBlob:
		return nil, errBlobNotImplemented
	default:
		return nil, fmt.Errorf("unknown resource type: %s", resource.Type)
	}

	// validate the ApplicationSpec before returning
	if err := appSpec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid application spec: %w", err)
	}

	return appSpec, nil
}

// buildResourcesSpec builds ResourcesSpec, using deployment-time
// overrides if present, otherwise falling back to the target region's defaults from ServiceSpec
func buildResourcesSpec(
	serviceSpec *resourcev1.ServiceSpec,
	deploymentSpec *deploymentv1.DeploymentSpec,
	targetRegion string,
) (*locoControllerV1.ResourcesSpec, error) {
	if serviceSpec == nil {
		return nil, errServiceSpecRequired
	}

	// Get the target region to extract default resources
	regionTarget, ok := serviceSpec.GetRegions()[targetRegion]
	if !ok {
		return nil, fmt.Errorf("target region %s not found in service spec", targetRegion)
	}

	// Start with region-specific defaults
	cpu := regionTarget.GetCpu()
	memory := regionTarget.GetMemory()
	minReplicas := regionTarget.GetMinReplicas()
	maxReplicas := regionTarget.GetMaxReplicas()
	scalers := regionTarget.GetScalers()

	// Override with deployment-time values if provided
	if deploymentSpec != nil {
		deploymentSvc := deploymentSpec.GetService()
		if deploymentSvc != nil {
			if deploymentSvc.Cpu != nil && deploymentSvc.GetCpu() != "" {
				cpu = deploymentSvc.GetCpu()
			}
			if deploymentSvc.Memory != nil && deploymentSvc.GetMemory() != "" {
				memory = deploymentSvc.GetMemory()
			}
			if deploymentSvc.MinReplicas != nil && deploymentSvc.GetMinReplicas() > 0 {
				minReplicas = deploymentSvc.GetMinReplicas()
			}
			if deploymentSvc.MaxReplicas != nil && deploymentSvc.GetMaxReplicas() > 0 {
				maxReplicas = deploymentSvc.GetMaxReplicas()
			}
			if deploymentSvc.GetScalers() != nil {
				scalers = deploymentSvc.GetScalers()
			}
		}
	}

	// Build ResourcesSpec with merged values
	resourcesSpec := &locoControllerV1.ResourcesSpec{
		CPU:    cpu,
		Memory: memory,
		Replicas: locoControllerV1.ReplicasSpec{
			Min: minReplicas,
			Max: maxReplicas,
		},
	}

	// Add scalers if configured
	if scalers != nil {
		resourcesSpec.Scalers = locoControllerV1.ScalersSpec{
			Enabled:      scalers.GetEnabled(),
			CPUTarget:    scalers.GetCpuTarget(),
			MemoryTarget: scalers.GetMemoryTarget(),
		}
	}

	return resourcesSpec, nil
}
