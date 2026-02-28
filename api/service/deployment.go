package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/commandbus"
	"github.com/team-loco/loco/api/pkg/converter"
	timeutil "github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrDeploymentNotFound = errors.New("deployment not found")
	ErrInvalidImage       = errors.New("invalid image reference")
	ErrInvalidPort        = errors.New("invalid port")
	ErrInvalidReplicas    = errors.New("replicas must be >= 1")
)

var imagePattern = regexp.MustCompile(`^([a-z0-9\-._]+(/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+|@sha256:[a-f0-9]{64})?$`)

// DeployCommandPayload is the payload sent to agents for deploy commands.
type DeployCommandPayload struct {
	DeploymentID  string                            `json:"deployment_id"`
	ResourceID    string                            `json:"resource_id"`
	WorkspaceID   string                            `json:"workspace_id"`
	ResourceName  string                            `json:"resource_name"`
	ResourceType  string                            `json:"resource_type"`
	Region        string                            `json:"region"`
	Hostname      string                            `json:"hostname"`
	LocoNamespace string                            `json:"loco_namespace"`
	AppSpec       *locoControllerV1.ApplicationSpec `json:"app_spec"`
}

// DeleteCommandPayload is the payload sent to agents for delete commands.
type DeleteCommandPayload struct {
	DeploymentID  string `json:"deployment_id"`
	ResourceID    string `json:"resource_id"`
	LocoNamespace string `json:"loco_namespace"`
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
		CreatedAt:     timeutil.ParsePostgresTimestamp(d.CreatedAt.Time),
		UpdatedAt:     timeutil.ParsePostgresTimestamp(d.UpdatedAt.Time),
		SpecVersion:   d.SpecVersion,
		Message:       d.Message,
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
	locoNamespace string
	machine       *tvm.VendingMachine
	cmdBus        commandbus.CommandBus
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine, locoNamespace string, cmdBus commandbus.CommandBus) *DeploymentServer {
	return &DeploymentServer{
		db:            db,
		queries:       queries,
		locoNamespace: locoNamespace,
		machine:       machine,
		cmdBus:        cmdBus,
	}
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.CreateDeploymentRequest],
) (*connect.Response[deploymentv1.CreateDeploymentResponse], error) {
	r := req.Msg

	resourceId, err := uuid.Parse(r.GetResourceId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid resource id", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource id: %w", err))
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.CreateDeployment, r.GetResourceId())); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to create deployment", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
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

	domain, err := s.queries.GetDomainByResourceId(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	// Validate and get region
	region := r.GetRegion()
	if region == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("region is required"))
	}

	environmentID, err := uuid.Parse(r.GetEnvironmentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid environment_id: %w", err))
	}

	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", environmentID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Get active cluster for the specified region and environment tier
	cluster, err := s.queries.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
		Region: region,
		Tier:   env.EnvironmentType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster for region", "region", region, "tier", env.EnvironmentType, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("no active cluster available for region %s tier %s: %w", region, env.EnvironmentType, err))
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

	// Get resource region for deployment record
	resourceRegion, err := s.queries.GetResourceRegionByResourceAndRegion(ctx, genDb.GetResourceRegionByResourceAndRegionParams{
		ResourceID: resourceId,
		Region:     region,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource region", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resource region not found: %w", err))
	}

	// Create deployment transactionally, finalizing previous deployments in the same region
	deploymentID, err := createDeploymentWithCleanup(ctx, s.db, s.queries, genDb.CreateDeploymentParams{
		ResourceID:       resourceId,
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
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Build the Application spec for the agent
	appSpec, err := buildApplicationSpec(
		resource,
		resourceSpec,
		domain.Domain,
		mergedSpec,
		region,
		environmentID,
		env.Name,
		deploymentID,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build application spec", "error", err, "resourceId", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to build application spec: %w", err))
	}

	// Create command payload with all info the agent needs
	cmdPayload := DeployCommandPayload{
		DeploymentID:  deploymentID.String(),
		ResourceID:    resource.ID.String(),
		WorkspaceID:   resource.WorkspaceID.String(),
		ResourceName:  resource.Name,
		ResourceType:  string(resource.Type),
		Region:        region,
		Hostname:      domain.Domain,
		LocoNamespace: s.locoNamespace,
		AppSpec:       appSpec,
	}

	payloadJSON, err := json.Marshal(cmdPayload)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal command payload", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to marshal command payload: %w", err))
	}

	// Dispatch deploy command to the agent via CommandBus
	cmd := &commandbus.Command{
		ID:        uuid.NewString(),
		ClusterID: cluster.ID.String(),
		Type:      commandbus.CommandTypeDeploy,
		Payload:   payloadJSON,
		CreatedAt: time.Now(),
	}

	if err := s.cmdBus.Send(ctx, cmd); err != nil {
		slog.ErrorContext(ctx, "failed to dispatch deploy command", "cluster_id", cluster.ID, "error", err)
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no agent connected for cluster: %w", err))
	}

	slog.InfoContext(ctx, "deploy command dispatched", "command_id", cmd.ID, "cluster_id", cluster.ID, "deployment_id", deploymentID.String())

	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{DeploymentId: deploymentID.String()}), nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.GetDeploymentRequest],
) (*connect.Response[deploymentv1.GetDeploymentResponse], error) {
	r := req.Msg

	deploymentId, err := uuid.Parse(r.DeploymentId)
	if err != nil {
		slog.ErrorContext(ctx, "invalid deployment id", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid deployment id: %w", err))
	}

	deploymentData, err := s.queries.GetDeploymentByID(ctx, deploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deploymentData.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	// check if user has permission to get deployment (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.GetDeployment, resource.ID.String())); err != nil {
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	// check if requester has permission to list deployments (resource:read)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.ListDeployments, r.GetResourceId())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceId, err := uuid.Parse(r.GetResourceId())
	if err != nil {
		slog.ErrorContext(ctx, "invalid resource id", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource id: %w", err))
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, decodeErr := decodeCursor(r.GetPageToken())
		if decodeErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", decodeErr))
		}
		pageToken = pgtype.Text{
			String: cursorID,
			Valid:  true,
		}
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: resourceId,
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

	deploymentId, err := uuid.Parse(r.DeploymentId)
	if err != nil {
		slog.ErrorContext(ctx, "invalid deployment id", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid deployment id: %w", err))
	}

	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if verifyErr := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.DeleteDeployment, resource.ID.String())); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to delete deployment", "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, verifyErr)
	}

	// if this is the active deployment, delete the Application
	if deployment.IsActive {
		// Create delete command payload
		cmdPayload := DeleteCommandPayload{
			DeploymentID:  deployment.ID.String(),
			ResourceID:    resource.ID.String(),
			LocoNamespace: s.locoNamespace,
		}

		payloadJSON, err := json.Marshal(cmdPayload)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal delete command payload", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to marshal command payload: %w", err))
		}

		// Dispatch delete command to the agent via CommandBus
		cmd := &commandbus.Command{
			ID:        uuid.NewString(),
			ClusterID: deployment.ClusterID.String(),
			Type:      commandbus.CommandTypeDelete,
			Payload:   payloadJSON,
			CreatedAt: time.Now(),
		}

		if err := s.cmdBus.Send(ctx, cmd); err != nil {
			slog.ErrorContext(ctx, "failed to dispatch delete command", "cluster_id", deployment.ClusterID, "error", err)
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no agent connected for cluster: %w", err))
		}

		slog.InfoContext(ctx, "delete command dispatched", "command_id", cmd.ID, "cluster_id", deployment.ClusterID, "deployment_id", deployment.ID.String())
	}

	// mark deployment as inactive
	err = s.queries.MarkDeploymentNotActive(ctx, deploymentId)
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

	deploymentId, err := uuid.Parse(r.DeploymentId)
	if err != nil {
		slog.ErrorContext(ctx, "invalid deployment id", "error", err)
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid deployment id: %w", err))
	}

	resourceID, err := s.queries.GetDeploymentResourceID(ctx, deploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.StreamDeployment, resource.ID.String())); err != nil {
		slog.WarnContext(ctx, "unauthorized to stream deployment", "resourceId", resource.ID.String())
		return connect.NewError(connect.CodePermissionDenied, err)
	}

	lastStatus := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	if err := s.sendDeploymentEvent(ctx, stream, r.DeploymentId, &lastStatus); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.sendDeploymentEvent(ctx, stream, r.DeploymentId, &lastStatus); err != nil {
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
	deployment, err := s.queries.GetDeploymentByID(ctx, uuid.MustParse(deploymentID))
	if err != nil {
		slog.ErrorContext(ctx, "failed to get deployment", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	statusPhase := parseDeploymentPhase(deployment.Status)
	statusStr := string(deployment.Status)
	message := deployment.Message

	if statusStr != *lastStatus {
		event := &deploymentv1.WatchDeploymentResponse{
			DeploymentId: deploymentID,
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

// buildApplicationSpec builds the ApplicationSpec for the loco controller.
// This is used both for direct k8s calls and for agent command payloads.
func buildApplicationSpec(
	resource genDb.Resource,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	region string,
	environmentId uuid.UUID,
	environmentName string,
	deploymentId uuid.UUID,
) (*locoControllerV1.ApplicationSpec, error) {
	// convert proto to controller CRD types
	crdServiceDeploymentSpec := converter.ProtoToServiceDeploymentSpec(deploymentSpec)

	appSpec := &locoControllerV1.ApplicationSpec{
		ResourceId:      resource.ID.String(),
		WorkspaceId:     resource.WorkspaceID.String(),
		Region:          region,
		EnvironmentId:   environmentId.String(),
		EnvironmentName: environmentName,
		DeploymentId:    deploymentId.String(),
	}

	switch resource.Type {
	case genDb.ResourceTypeService:
		if resourceSpec.GetService() == nil {
			return nil, fmt.Errorf("resource spec missing service configuration")
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
		return nil, fmt.Errorf("database resource type not yet implemented")
	case genDb.ResourceTypeCache:
		return nil, fmt.Errorf("cache resource type not yet implemented")
	case genDb.ResourceTypeQueue:
		return nil, fmt.Errorf("queue resource type not yet implemented")
	case genDb.ResourceTypeBlob:
		return nil, fmt.Errorf("blob resource type not yet implemented")
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
