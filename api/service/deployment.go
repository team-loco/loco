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
	"github.com/loco-team/loco/api/contextkeys"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/kube"
	timeutil "github.com/loco-team/loco/api/timeutil"
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/controller/api/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db         *pgxpool.Pool
	queries    genDb.Querier
	kubeClient *kube.Client
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(db *pgxpool.Pool, queries genDb.Querier, kubeClient *kube.Client) *DeploymentServer {
	return &DeploymentServer{
		db:         db,
		queries:    queries,
		kubeClient: kubeClient,
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

	if r.Spec.Image == nil || *r.Spec.Image == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("image is required"))
	}

	if r.Spec.InitialReplicas == nil || *r.Spec.InitialReplicas < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidReplicas)
	}

	if !imagePattern.MatchString(*r.Spec.Image) {
		slog.WarnContext(ctx, "invalid image format", "image", *r.Spec.Image)
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidImage)
	}

	replicas := *r.Spec.InitialReplicas

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	workspaceID := resource.WorkspaceID

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

	specJSON, err := json.Marshal(r.Spec)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	err = s.queries.MarkPreviousDeploymentsNotCurrent(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark previous deployments not current", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deployment, err := s.queries.CreateDeployment(ctx, genDb.CreateDeploymentParams{
		ResourceID:  r.ResourceId,
		ClusterID:   cluster.ID,
		Image:       *r.Spec.Image,
		Replicas:    replicas,
		Status:      genDb.DeploymentStatusPending,
		IsCurrent:   true,
		CreatedBy:   userID,
		Spec:        specJSON,
		SpecVersion: pgtype.Int4{Int32: 1, Valid: true},
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// create LocoResource in loco-system namespace
	err = createLocoResource(ctx, s.kubeClient, resource, deployment)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create LocoResource", "error", err, "resource_id", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create LocoResource: %w", err))
	}
	slog.InfoContext(ctx, "created LocoResource", "resource_id", resource.ID, "resource_name", resource.Name)

	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{
		DeploymentId: deployment.ID,
		Message:      "Successfully scheduled deployment.",
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

	deploymentResp := &deploymentv1.Deployment{
		Id:          deploymentData.ID,
		ResourceId:  deploymentData.ResourceID,
		Image:       deploymentData.Image,
		Replicas:    deploymentData.Replicas,
		Status:      parseDeploymentPhase(deploymentData.Status),
		IsCurrent:   deploymentData.IsCurrent,
		CreatedBy:   deploymentData.CreatedBy,
		CreatedAt:   timeutil.ParsePostgresTimestamp(deploymentData.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(deploymentData.UpdatedAt.Time),
		SpecVersion: deploymentData.SpecVersion.Int32,
	}

	if len(deploymentData.Spec) > 0 {
		var spec map[string]any
		if err := json.Unmarshal(deploymentData.Spec, &spec); err == nil {
			if s, err := structpb.NewStruct(spec); err == nil {
				deploymentResp.Spec = s
			}
		}
	}

	if deploymentData.Message.Valid {
		deploymentResp.Message = &deploymentData.Message.String
	}
	if deploymentData.ErrorMessage.Valid {
		deploymentResp.ErrorMessage = &deploymentData.ErrorMessage.String
	}
	if deploymentData.StartedAt.Valid {
		ts := timeutil.ParsePostgresTimestamp(deploymentData.StartedAt.Time)
		deploymentResp.StartedAt = ts
	}
	if deploymentData.CompletedAt.Valid {
		ts := timeutil.ParsePostgresTimestamp(deploymentData.CompletedAt.Time)
		deploymentResp.CompletedAt = ts
	}

	return connect.NewResponse(&deploymentv1.GetDeploymentResponse{
		Deployment: deploymentResp,
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
		deployment := &deploymentv1.Deployment{
			Id:          d.ID,
			ResourceId:  d.ResourceID,
			ClusterId:   d.ClusterID,
			Image:       d.Image,
			Replicas:    d.Replicas,
			Status:      parseDeploymentPhase(d.Status),
			IsCurrent:   d.IsCurrent,
			CreatedBy:   d.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(d.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(d.UpdatedAt.Time),
			SpecVersion: d.SpecVersion.Int32,
		}

		if len(d.Spec) > 0 {
			var spec map[string]any
			if err := json.Unmarshal(d.Spec, &spec); err == nil {
				if s, err := structpb.NewStruct(spec); err == nil {
					deployment.Spec = s
				}
			}
		}

		if d.Message.Valid {
			deployment.Message = &d.Message.String
		}
		if d.ErrorMessage.Valid {
			deployment.ErrorMessage = &d.ErrorMessage.String
		}
		if d.StartedAt.Valid {
			ts := timeutil.ParsePostgresTimestamp(d.StartedAt.Time)
			deployment.StartedAt = ts
		}
		if d.CompletedAt.Valid {
			ts := timeutil.ParsePostgresTimestamp(d.CompletedAt.Time)
			deployment.CompletedAt = ts
		}

		deployments = append(deployments, deployment)
	}

	return connect.NewResponse(&deploymentv1.ListDeploymentsResponse{
		Deployments: deployments,
		Total:       total,
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

		if deployment.ErrorMessage.Valid {
			event.ErrorMessage = &deployment.ErrorMessage.String
		}

		if err := stream.Send(event); err != nil {
			return err
		}

		*lastStatus = statusStr
		slog.InfoContext(ctx, "sent deployment event", "deployment_id", deploymentID, "status", statusStr)
	}

	return nil
}

// createLocoResource creates a LocoResource in the loco-system namespace
func createLocoResource(
	ctx context.Context,
	kubeClient *kube.Client,
	resource genDb.Resource,
	deployment genDb.Deployment,
) error {
	// parse deployment spec
	var deploymentSpec map[string]any
	if len(deployment.Spec) > 0 {
		if err := json.Unmarshal(deployment.Spec, &deploymentSpec); err != nil {
			slog.WarnContext(ctx, "failed to parse deployment spec", "error", err)
			deploymentSpec = make(map[string]any)
		}
	} else {
		deploymentSpec = make(map[string]any)
	}

	// parse resource spec
	var resourceSpec map[string]any
	if len(resource.Spec) > 0 {
		if err := json.Unmarshal(resource.Spec, &resourceSpec); err != nil {
			slog.WarnContext(ctx, "failed to parse resource spec", "error", err)
			resourceSpec = make(map[string]any)
		}
	} else {
		resourceSpec = make(map[string]any)
	}

	// build LocoResource
	locoRes := &locoControllerV1.LocoResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("resource-%d-%s", resource.ID, resource.Name),
			Namespace: "loco-system",
			Labels:    map[string]string{},
		},
		Spec: locoControllerV1.LocoResourceSpec{
			ResourceId:  resource.ID,
			WorkspaceID: resource.WorkspaceID,
			Type:        "SERVICE",
			Deployment: &locoControllerV1.DeploymentSpec{
				Image:           deployment.Image,
				InitialReplicas: deployment.Replicas,
				Env:             parseEnvFromSpec(deploymentSpec),
				CreatedBy:       deployment.CreatedBy,
			},
		},
	}

	// create the LocoResource
	if err := kubeClient.ControllerClient.Create(ctx, locoRes); err != nil {
		slog.ErrorContext(ctx, "failed to create LocoResource", "error", err, "resource_id", resource.ID)
		return err
	}

	return nil
}

// parseEnvFromSpec extracts environment variables from deployment spec
func parseEnvFromSpec(spec map[string]any) map[string]string {
	env := make(map[string]string)
	if envData, ok := spec["env"].(map[string]any); ok {
		for k, v := range envData {
			if strVal, ok := v.(string); ok {
				env[k] = strVal
			}
		}
	}
	return env
}
