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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/kube"
	timeutil "github.com/team-loco/loco/api/timeutil"
	deploymentv1 "github.com/team-loco/loco/shared/proto/deployment/v1"
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
		return deploymentv1.DeploymentPhase_PENDING
	case genDb.DeploymentStatusDeploying:
		return deploymentv1.DeploymentPhase_DEPLOYING
	case genDb.DeploymentStatusRunning:
		return deploymentv1.DeploymentPhase_RUNNING
	case genDb.DeploymentStatusSucceeded:
		return deploymentv1.DeploymentPhase_SUCCEEDED
	case genDb.DeploymentStatusFailed:
		return deploymentv1.DeploymentPhase_FAILED
	case genDb.DeploymentStatusCanceled:
		return deploymentv1.DeploymentPhase_CANCELED
	default:
		return deploymentv1.DeploymentPhase_UNSPECIFIED
	}
}

func deploymentToProto(d genDb.Deployment, resourceType string) *deploymentv1.Deployment {
	deployment := &deploymentv1.Deployment{
		Id:          d.ID,
		ResourceId:  d.ResourceID,
		ClusterId:   d.ClusterID,
		Replicas:    d.Replicas,
		Status:      parseDeploymentPhase(d.Status),
		IsActive:    d.IsActive,
		CreatedBy:   d.CreatedBy,
		CreatedAt:   timeutil.ParsePostgresTimestamp(d.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(d.UpdatedAt.Time),
		SpecVersion: d.SpecVersion,
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

	if d.Message.Valid {
		deployment.Message = &d.Message.String
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
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(db *pgxpool.Pool, queries genDb.Querier, kubeClient *kube.Client, locoNamespace string) *DeploymentServer {
	return &DeploymentServer{
		db:            db,
		queries:       queries,
		kubeClient:    kubeClient,
		locoNamespace: locoNamespace,
	}
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.CreateDeploymentRequest],
) (*connect.Response[deploymentv1.CreateDeploymentResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// todo: move below validations to a dedicated validation package.
	if r.Spec == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec is required"))
	}

	// validate that request spec contains a service deployment (for now, only services are supported)
	if r.Spec.GetService() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service deployments are currently supported"))
	}

	serviceSpec := r.Spec.GetService()

	if serviceSpec.Build == nil || serviceSpec.Build.Image == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("image is required"))
	}

	if serviceSpec.Port < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidPort)
	}

	if !imagePattern.MatchString(serviceSpec.Build.Image) {
		slog.WarnContext(ctx, "invalid image format", "image", serviceSpec.Build.Image)
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidImage)
	}

	replicas := serviceSpec.GetMinReplicas()

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	workspaceID := resource.WorkspaceID

	domain, err := s.queries.GetDomainByResourceId(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	// todo: move membership check higher.
	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", workspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin && role != genDb.WorkspaceRoleDeploy {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin or have deploy role"))
	}

	// get active cluster
	cluster, err := s.queries.GetFirstActiveCluster(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("no active clusters available: %w", err))
	}

	// deserialize resource spec and merge with request spec
	resourceSpec, deserializeErr := converter.DeserializeResourceSpec(resource.Spec)
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	mergedSpec, mergeErr := converter.MergeDeploymentSpec(resourceSpec, r.Spec)
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

	err = s.queries.MarkPreviousDeploymentsNotActive(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark previous deployments not active", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deploymentID, err := s.queries.CreateDeployment(ctx, genDb.CreateDeploymentParams{
		ResourceID:  r.ResourceId,
		ClusterID:   cluster.ID,
		Replicas:    replicas,
		Status:      genDb.DeploymentStatusPending,
		IsActive:    true,
		CreatedBy:   userID,
		Spec:        specJSON,
		SpecVersion: 1,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// create Application in loco-system namespace (pass merged spec WITH env to controller)
	err = createLocoResource(ctx, s.kubeClient, resource, resourceSpec, domain.Domain, mergedSpec, s.locoNamespace)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create Application", "error", err, "resource_id", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create Application: %w", err))
	}
	slog.InfoContext(ctx, "created/updated Application", "resource_id", resource.ID, "resource_name", resource.Name)

	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{
		DeploymentId: deploymentID,
		Message:      "Deployment scheduled.",
	}), nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.GetDeploymentRequest],
) (*connect.Response[deploymentv1.GetDeploymentResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

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

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	return connect.NewResponse(&deploymentv1.GetDeploymentResponse{
		Deployment: deploymentToProto(deploymentData, string(resource.Type)),
	}), nil
}

// ListDeployments lists deployments for an app
func (s *DeploymentServer) ListDeployments(
	ctx context.Context,
	req *connect.Request[deploymentv1.ListDeploymentsRequest],
) (*connect.Response[deploymentv1.ListDeploymentsResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	limit := r.GetLimit()
	if limit == 0 {
		limit = 50
	}
	offset := r.GetOffset()

	total, err := s.queries.CountDeploymentsForResource(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: r.ResourceId,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployments = append(deployments, deploymentToProto(d, string(resource.Type)))
	}

	return connect.NewResponse(&deploymentv1.ListDeploymentsResponse{
		Deployments: deployments,
		Total:       total,
	}), nil
}

// DeleteDeployment deletes/inactivates a deployment and cleans up its Application
func (s *DeploymentServer) DeleteDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.DeleteDeploymentRequest],
) (*connect.Response[deploymentv1.DeleteDeploymentResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

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

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	// if this is the active deployment, delete the Application
	if deployment.IsActive {
		if err := deleteLocoResource(ctx, s.kubeClient, resource.ID, s.locoNamespace); err != nil {
			slog.ErrorContext(ctx, "failed to delete Application", "error", err, "resource_id", resource.ID)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to cleanup Application: %w", err))
		}
	}

	// mark deployment as inactive
	err = s.queries.MarkDeploymentNotActive(ctx, r.DeploymentId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark deployment not active", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&deploymentv1.DeleteDeploymentResponse{
		DeploymentId: r.DeploymentId,
		Message:      "Deployment deleted.",
	}), nil
}

// StreamDeployment streams deployment status updates
func (s *DeploymentServer) StreamDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.StreamDeploymentRequest],
	stream *connect.ServerStream[deploymentv1.DeploymentEvent],
) error {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

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

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
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
	stream *connect.ServerStream[deploymentv1.DeploymentEvent],
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
	message := ""
	if deployment.Message.Valid {
		message = deployment.Message.String
	}

	if statusStr != *lastStatus {
		event := &deploymentv1.DeploymentEvent{
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
	spec *deploymentv1.DeploymentSpec,
	locoNamespace string,
) error {
	// convert proto to controller CRD types (includes all fields: image, replicas, cpu, memory, env, healthCheck, metrics, etc.)
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(spec)
	slog.InfoContext(ctx, "converted deployment spec", "image", crdServiceDeploymentSpec.Image, "port", crdServiceDeploymentSpec.Port)

	locoResourceSpec := locoControllerV1.ApplicationSpec{
		ResourceId:  resource.ID,
		WorkspaceId: resource.WorkspaceID,
	}

	switch specType := resourceSpec.Spec.(type) {
	case *resourcev1.ResourceSpec_Service:
		locoResourceSpec.Type = "SERVICE"
		resourcesSpec, err := buildResourcesSpecFromServiceSpec(specType.Service)
		if err != nil {
			return fmt.Errorf("failed to build resources spec: %w", err)
		}
		locoResourceSpec.ServiceSpec = &locoControllerV1.ServiceSpec{
			Deployment: crdServiceDeploymentSpec,
			Resources:  resourcesSpec,
			Obs:        converter.ProtoToObsSpec(specType.Service.GetObservability()),
			Routing:    converter.ProtoToRoutingSpec(specType.Service.GetRouting(), hostname),
		}

	case *resourcev1.ResourceSpec_Database:
		// TODO: implement database resource type
		return fmt.Errorf("database resource type not yet implemented")
	case *resourcev1.ResourceSpec_Cache:
		// TODO: implement cache resource type
		return fmt.Errorf("cache resource type not yet implemented")
	case *resourcev1.ResourceSpec_Queue:
		// TODO: implement queue resource type
		return fmt.Errorf("queue resource type not yet implemented")
	case *resourcev1.ResourceSpec_Blob:
		// TODO: implement blob resource type
		return fmt.Errorf("blob resource type not yet implemented")
	default:
		return fmt.Errorf("unknown or unset resource spec type")
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

	// create or update the Application
	err := kubeClient.ControllerClient.Get(ctx, client.ObjectKey{
		Name:      locoRes.Name,
		Namespace: locoRes.Namespace,
	}, locoRes)

	if err == nil {
		// resource exists, update it
		if err := kubeClient.ControllerClient.Update(ctx, locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to update Application", "error", err, "resource_id", resource.ID)
			return err
		}
		slog.InfoContext(ctx, "updated existing Application", "resource_id", resource.ID)
	} else if client.IgnoreNotFound(err) == nil {
		// resource does not exist, create it
		if err := kubeClient.ControllerClient.Create(ctx, locoRes); err != nil {
			slog.ErrorContext(ctx, "failed to create Application", "error", err, "resource_id", resource.ID)
			return err
		}
		slog.InfoContext(ctx, "created new Application", "resource_id", resource.ID)
	} else {
		// some other error occurred
		slog.ErrorContext(ctx, "failed to check if Application exists", "error", err, "resource_id", resource.ID)
		return err
	}

	return nil
}

// buildResourcesSpecFromServiceSpec extracts ResourcesSpec from ServiceSpec defaults
// This extracts the default resource configuration (CPU, Memory, Replicas, Scalers)
// which can be overridden at deployment time via DeploymentSpec
func buildResourcesSpecFromServiceSpec(serviceSpec *resourcev1.ServiceSpec) (*locoControllerV1.ResourcesSpec, error) {
	if serviceSpec == nil {
		return nil, fmt.Errorf("service spec is required")
	}

	// NOTE: Observability settings (Logging, Metrics, Tracing) from ServiceSpec are currently
	// not propagated to the controller. They can be added to LocoResourceSpec if needed.

	// Get the primary region to extract default resources
	var primaryRegion *resourcev1.RegionTarget
	for _, region := range serviceSpec.GetRegions() {
		if region.Primary {
			primaryRegion = region
			break
		}
	}

	if primaryRegion == nil {
		return nil, fmt.Errorf("primary region not found in service spec")
	}

	// Build ResourcesSpec from primary region defaults
	resourcesSpec := &locoControllerV1.ResourcesSpec{
		CPU:    primaryRegion.Cpu,
		Memory: primaryRegion.Memory,
		Replicas: locoControllerV1.ReplicasSpec{
			Min: primaryRegion.MinReplicas,
			Max: primaryRegion.MaxReplicas,
		},
	}

	// Add scalers if configured
	if primaryRegion.Scalers != nil {
		resourcesSpec.Scalers = locoControllerV1.ScalersSpec{
			Enabled:      primaryRegion.Scalers.GetEnabled(),
			CPUTarget:    primaryRegion.Scalers.GetCpuTarget(),
			MemoryTarget: primaryRegion.Scalers.GetMemoryTarget(),
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
			slog.ErrorContext(ctx, "failed to delete Application", "error", err, "resource_id", resourceID)
			return err
		}
	}
	slog.InfoContext(ctx, "deleted Application", "resource_id", resourceID)
	return nil
}
