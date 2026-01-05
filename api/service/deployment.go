package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/kube"
	timeutil "github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
	deploymentv1 "github.com/team-loco/loco/shared/proto/deployment/v1"
	resourcev1 "github.com/team-loco/loco/shared/proto/resource/v1"
	"github.com/team-loco/loco/shared/version"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	ErrDeploymentNotFound = errors.New("deployment not found")
	ErrInvalidImage       = errors.New("invalid image reference")
	ErrInvalidPort        = errors.New("invalid port")
	ErrInvalidReplicas    = errors.New("replicas must be >= 1")
)

var imagePattern = regexp.MustCompile(`^([a-z0-9\-._]+(/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+|@sha256:[a-f0-9]{64})?$`)

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
		Id:          d.ID,
		ResourceId:  d.ResourceID,
		ClusterId:   d.ClusterID,
		Region:      d.Region,
		Replicas:    d.Replicas,
		Status:      parseDeploymentPhase(d.Status),
		IsActive:    d.IsActive,
		CreatedAt:   timeutil.ParsePostgresTimestamp(d.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(d.UpdatedAt.Time),
		SpecVersion: d.SpecVersion,
		Message:     d.Message,
	}

	if len(d.Spec) > 0 {
		spec := &deploymentv1.DeploymentSpec{}

		switch resourceType {
		case "service":
			serviceSpec := &deploymentv1.ServiceDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, serviceSpec); err != nil {
				slog.WarnContext(context.Background(), "failed to unmarshal service deployment spec", "error", err, "deployment_id", d.ID)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Service{Service: serviceSpec}
			}
		case "database":
			databaseSpec := &deploymentv1.DatabaseDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, databaseSpec); err != nil {
				slog.WarnContext(context.Background(), "failed to unmarshal database deployment spec", "error", err, "deployment_id", d.ID)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Database{Database: databaseSpec}
			}
		case "cache":
			cacheSpec := &deploymentv1.CacheDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, cacheSpec); err != nil {
				slog.WarnContext(context.Background(), "failed to unmarshal cache deployment spec", "error", err, "deployment_id", d.ID)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Cache{Cache: cacheSpec}
			}
		case "queue":
			queueSpec := &deploymentv1.QueueDeploymentSpec{}
			if err := protojson.Unmarshal(d.Spec, queueSpec); err != nil {
				slog.WarnContext(context.Background(), "failed to unmarshal queue deployment spec", "error", err, "deployment_id", d.ID)
			} else {
				spec.Spec = &deploymentv1.DeploymentSpec_Queue{Queue: queueSpec}
			}
		default:
			slog.WarnContext(context.Background(), "unknown resource type", "resource_type", resourceType, "deployment_id", d.ID)
		}

		deployment.Spec = spec
	}

	if d.StartedAt.Valid {
		ts := timeutil.ParsePostgresTimestamp(d.StartedAt.Time)
		deployment.StartedAt = ts
	}
	if d.CompletedAt.Valid {
		ts := timeutil.ParsePostgresTimestamp(d.CompletedAt.Time)
		deployment.CompletedAt = ts
	}

	return deployment
}

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db            *pgxpool.Pool
	queries       genDb.Querier
	kubeClient    *kube.Client
	locoNamespace string
	machine       *tvm.VendingMachine
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine, kubeClient *kube.Client, locoNamespace string) *DeploymentServer {
	return &DeploymentServer{
		db:            db,
		queries:       queries,
		kubeClient:    kubeClient,
		locoNamespace: locoNamespace,
		machine:       machine,
	}
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.CreateDeploymentRequest],
) (*connect.Response[deploymentv1.CreateDeploymentResponse], error) {
	r := req.Msg

	resource, err := s.queries.GetResourceByID(ctx, r.GetResourceId())
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.CreateDeployment, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to create deployment", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// todo: move below validations to a dedicated validation package.
	if r.GetSpec() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec is required"))
	}

	// validate that request spec contains a service deployment (for now, only services are supported)
	if r.GetSpec().GetService() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service deployments are currently supported"))
	}

	serviceSpec := r.GetSpec().GetService()

	if serviceSpec.GetBuild() == nil || serviceSpec.GetBuild().GetImage() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("image is required"))
	}

	if serviceSpec.GetPort() < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidPort)
	}

	if !imagePattern.MatchString(serviceSpec.GetBuild().GetImage()) {
		slog.WarnContext(ctx, "invalid image format", "image", serviceSpec.GetBuild().GetImage())
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidImage)
	}

	replicas := serviceSpec.GetMinReplicas()

	domain, err := s.queries.GetDomainByResourceId(ctx, r.GetResourceId())
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	// Validate and get region
	region := r.GetRegion()
	if region == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("region is required"))
	}
	// Get active cluster for the specified region
	cluster, err := s.queries.GetActiveClusterByRegion(ctx, region)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster for region", "region", region, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("no active cluster available for region %s: %w", region, err))
	}

	// deserialize resource spec and merge with request spec
	resourceSpec, deserializeErr := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	mergedSpec, mergeErr := converter.MergeDeploymentSpec(resourceSpec, r.GetSpec(), region)
	if mergeErr != nil {
		slog.ErrorContext(ctx, mergeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("merge error: %w", mergeErr))
	}

	// create spec copy without env for DB persistence (no plaintext secrets in DB)
	mergedServiceSpec := mergedSpec.GetService()

	// create shallow copy excluding env as it can have sensitive info.
	// todo: consider using dedicated secrets management solution.
	specForDBService := mergedServiceSpec
	specForDBService.Env = nil

	specJSON, err := json.Marshal(specForDBService)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	// Create deployment transactionally, finalizing previous deployments in the same region
	deploymentID, err := createDeploymentWithCleanup(ctx, s.db, s.queries, genDb.CreateDeploymentParams{
		ResourceID:  r.GetResourceId(),
		ClusterID:   cluster.ID,
		Region:      region,
		Replicas:    replicas,
		Status:      genDb.DeploymentStatusPending,
		IsActive:    true,
		Message:     "Scheduling deployment",
		Spec:        specJSON,
		SpecVersion: version.SpecVersionV1,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// create Application in loco-system namespace (pass merged spec WITH env to controller)
	err = createLocoResource(ctx, s.kubeClient, resource, resourceSpec, domain.Domain, mergedSpec, s.locoNamespace, region)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create Application", "error", err, "resourceId", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create Application: %w", err))
	}
	slog.InfoContext(ctx, "created/updated Application", "resourceId", resource.ID, "resource_name", resource.Name)

	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get created deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{DeploymentId: deployment.ID}), nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.GetDeploymentRequest],
) (*connect.Response[deploymentv1.GetDeploymentResponse], error) {
	r := req.Msg

	deploymentData, err := s.queries.GetDeploymentByID(ctx, r.DeploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deploymentData.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// check if user has permission to get deployment (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.GetDeployment, resource.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to get deployment", "resourceId", resource.ID)
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

	// check if requester has permission to list deployments (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.ListDeployments, r.GetResourceId())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.GetResourceId())
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = pgtype.Text{
			String: fmt.Sprintf("%d", cursorID),
			Valid:  true,
		}
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: r.GetResourceId(),
		Limit:      pageSize,
		PageToken:  pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployments = append(deployments, deploymentToProto(d, string(resource.Type)))
	}

	var nextPageToken string
	if len(deploymentList) == int(pageSize) {
		nextPageToken = encodeCursor(deploymentList[len(deploymentList)-1].ID)
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

	deployment, err := s.queries.GetDeploymentByID(ctx, r.DeploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.DeleteDeployment, resource.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete deployment", "resourceId", resource.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// if this is the active deployment, delete the Application
	if deployment.IsActive {
		if err := deleteLocoResource(ctx, s.kubeClient, resource.ID, s.locoNamespace); err != nil {
			slog.ErrorContext(ctx, "failed to delete Application", "error", err, "resourceId", resource.ID)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to cleanup Application: %w", err))
		}
	}

	// mark deployment as inactive
	err = s.queries.MarkDeploymentNotActive(ctx, r.DeploymentId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark deployment not active", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&deploymentv1.DeleteDeploymentResponse{}), nil
}

// WatchDeployment streams deployment status updates
func (s *DeploymentServer) WatchDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.WatchDeploymentRequest],
	stream *connect.ServerStream[deploymentv1.WatchDeploymentResponse],
) error {
	r := req.Msg

	resourceID, err := s.queries.GetDeploymentResourceID(ctx, r.DeploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.StreamDeployment, resource.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to stream deployment", "resourceId", resource.ID)
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	lastStatus := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	if err := s.sendDeploymentEvent(ctx, stream, fmt.Sprintf("%d", r.DeploymentId), &lastStatus); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.sendDeploymentEvent(ctx, stream, fmt.Sprintf("%d", r.DeploymentId), &lastStatus); err != nil {
				return err
			}

			if lastStatus == "succeeded" || lastStatus == "failed" {
				return nil
			}
		}
	}
}

func (s *DeploymentServer) sendDeploymentEvent(
	ctx context.Context,
	stream *connect.ServerStream[deploymentv1.WatchDeploymentResponse],
	deploymentID string,
	lastStatus *string,
) error {
	parsedDeploymentID, err := strconv.ParseInt(deploymentID, 10, 64)
	if err != nil {
		slog.ErrorContext(ctx, "invalid deployment ID", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("invalid deployment ID: %w", err))
	}

	deployment, err := s.queries.GetDeploymentByID(ctx, parsedDeploymentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get deployment", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	statusPhase := parseDeploymentPhase(deployment.Status)
	statusStr := string(deployment.Status)
	message := deployment.Message

	if statusStr != *lastStatus {
		event := &deploymentv1.WatchDeploymentResponse{
			DeploymentId: parsedDeploymentID,
			Status:       statusPhase,
			Message:      message,
			Timestamp:    timestamppb.New(time.Now()),
		}

		if err := stream.Send(event); err != nil {
			return err
		}

		*lastStatus = statusStr
		slog.InfoContext(ctx, "sent deployment event", "deployment_id", deploymentID, "status", statusStr)
	}

	return nil
}

// createLocoResource creates a Application in the loco-system namespace
func createLocoResource(
	ctx context.Context,
	kubeClient *kube.Client,
	resource genDb.Resource,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	locoNamespace string,
	region string,
) error {
	// convert proto to controller CRD types
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(deploymentSpec)
	slog.InfoContext(ctx, "converted deployment spec", "image", crdServiceDeploymentSpec.Image, "port", crdServiceDeploymentSpec.Port)

	locoResourceSpec := locoControllerV1.ApplicationSpec{
		ResourceId:  resource.ID,
		WorkspaceId: resource.WorkspaceID,
		Region:      region,
	}

	switch resource.Type {
	case genDb.ResourceTypeService:
		if resourceSpec.GetService() == nil {
			return fmt.Errorf("resource spec missing service configuration")
		}
		locoResourceSpec.Type = "SERVICE"
		resourcesSpec, err := buildResourcesSpec(resourceSpec.GetService(), deploymentSpec, region)
		if err != nil {
			return fmt.Errorf("failed to build resources spec: %w", err)
		}
		locoResourceSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  resourcesSpec,
			Obs:        converter.ProtoToObsSpec(resourceSpec.GetService().GetObservability()),
			Routing:    converter.ProtoToRoutingSpec(resourceSpec.GetService().GetRouting(), hostname),
		}

	case genDb.ResourceTypeDatabase:
		// TODO: implement database resource type
		return fmt.Errorf("database resource type not yet implemented")
	case genDb.ResourceTypeCache:
		// TODO: implement cache resource type
		return fmt.Errorf("cache resource type not yet implemented")
	case genDb.ResourceTypeQueue:
		// TODO: implement queue resource type
		return fmt.Errorf("queue resource type not yet implemented")
	case genDb.ResourceTypeBlob:
		// TODO: implement blob resource type
		return fmt.Errorf("blob resource type not yet implemented")
	default:
		return fmt.Errorf("unknown resource type: %s", resource.Type)
	}

	// validate the ApplicationSpec before creating/updating
	if err := locoResourceSpec.Validate(); err != nil {
		slog.ErrorContext(ctx, "failed to validate application spec", "error", err)
		return fmt.Errorf("invalid application spec: %w", err)
	}

	// build Application

	locoRes := &locoControllerV1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("resource-%d", resource.ID),
			Namespace: locoNamespace,
			Labels:    map[string]string{},
		},
		Spec: locoResourceSpec,
	}

	specJSON, _ := json.MarshalIndent(locoResourceSpec, "", "  ")
	slog.InfoContext(ctx, "building Application", "resourceId", resource.ID, "spec", string(specJSON))

	// create or update the Application
	err := kubeClient.ControllerClient.Get(ctx, client.ObjectKey{
		Name:      locoRes.Name,
		Namespace: locoRes.Namespace,
	}, locoRes)

	if err == nil {
		// resource exists, update it
		if err := kubeClient.ControllerClient.Update(ctx, locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to update Application", "error", err, "resourceId", resource.ID)
			return err
		}
		slog.InfoContext(ctx, "updated existing Application", "resourceId", resource.ID)
	} else if client.IgnoreNotFound(err) == nil {
		// resource does not exist, create it
		if err := kubeClient.ControllerClient.Create(ctx, locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to create Application", "error", err, "resourceId", resource.ID)
			return err
		}
		slog.InfoContext(ctx, "created new Application", "resourceId", resource.ID)
	} else {
		// some other error occurred
		slog.ErrorContext(ctx, "failed to check if Application exists", "error", err, "resourceId", resource.ID)
		return err
	}

	return nil
}

// buildResourcesSpec builds ResourcesSpec, using deployment-time
// overrides if present, otherwise falling back to the target region's defaults from ServiceSpec
func buildResourcesSpec(
	serviceSpec *resourcev1.ServiceSpec,
	deploymentSpec *deploymentv1.DeploymentSpec,
	targetRegion string,
) (*locoControllerV1.ResourcesSpec, error) {
	if serviceSpec == nil {
		return nil, fmt.Errorf("service spec is required")
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
			if deploymentSvc.Scalers != nil {
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

// deleteLocoResource deletes a Application from the loco-system namespace
func deleteLocoResource(ctx context.Context, kubeClient *kube.Client, resourceID int64, locoNamespace string) error {
	locoRes := &locoControllerV1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("resource-%d", resourceID),
			Namespace: locoNamespace,
		},
	}

	if err := kubeClient.ControllerClient.Delete(ctx, locoRes); err != nil {
		if client.IgnoreNotFound(err) != nil {
			slog.ErrorContext(ctx, "failed to delete Application", "error", err, "resourceId", resourceID)
			return err
		}
	}
	slog.InfoContext(ctx, "deleted Application", "resourceId", resourceID)
	return nil
}
