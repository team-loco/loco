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
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/kube"
	timeutil "github.com/loco-team/loco/api/timeutil"
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db         *pgxpool.Pool
	queries    *genDb.Queries
	kubeClient *kube.Client
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(db *pgxpool.Pool, queries *genDb.Queries, kubeClient *kube.Client) *DeploymentServer {
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

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// tood: move all validations into some sort of hook.

	replicas := r.GetReplicas()
	if replicas < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidReplicas)
	}

	if !imagePattern.MatchString(r.Image) {
		slog.WarnContext(ctx, "invalid image format", "image", r.Image)
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidImage)
	}

	if len(r.Ports) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one port is required"))
	}

	// todo: values shared with loader.go, but we should technically move these into constants
	for _, port := range r.Ports {
		if port.Port < 1024 || port.Port > 65535 {
			return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidPort)
		}
		if port.Protocol != "TCP" && port.Protocol != "UDP" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("protocol must be TCP or UDP"))
		}
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "app_id", r.AppId)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}
	workspaceID := app.WorkspaceID

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

	// todo: assign cluster and verify health

	config := map[string]any{
		"env":       r.Env,
		"ports":     r.Ports,
		"resources": r.Resources,
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal config", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid config: %w", err))
	}

	err = s.queries.MarkPreviousDeploymentsNotCurrent(ctx, r.AppId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark previous deployments not current", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deployment, err := s.queries.CreateDeployment(ctx, genDb.CreateDeploymentParams{
		AppID:         r.AppId,
		ClusterID:     1,
		Image:         r.Image,
		Replicas:      replicas,
		Status:        genDb.DeploymentStatusPending,
		IsCurrent:     true,
		CreatedBy:     userID,
		Config:        configJSON,
		SchemaVersion: pgtype.Int4{Int32: 1, Valid: true},
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	go s.allocateDeployment(context.Background(), &app, &deployment, r.Env)

	deploymentResp := &deploymentv1.Deployment{
		Id:        deployment.ID,
		AppId:     deployment.AppID,
		Image:     deployment.Image,
		Replicas:  deployment.Replicas,
		Status:    *deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING.Enum(),
		IsCurrent: deployment.IsCurrent,
		CreatedBy: deployment.CreatedBy,
		CreatedAt: timeutil.ParsePostgresTimestamp(deployment.CreatedAt.Time),
		UpdatedAt: timeutil.ParsePostgresTimestamp(deployment.UpdatedAt.Time),
	}

	if len(deployment.Config) > 0 {
		configStr := string(deployment.Config)
		deploymentResp.Config = &configStr
	}
	deploymentResp.SchemaVersion = deployment.SchemaVersion.Int32

	if deployment.Message.Valid {
		deploymentResp.Message = &deployment.Message.String
	}

	// todo: a message for this would be nice.
	return connect.NewResponse(&deploymentv1.CreateDeploymentResponse{
		Deployment: deploymentResp,
	}), nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *connect.Request[deploymentv1.GetDeploymentRequest],
) (*connect.Response[deploymentv1.GetDeploymentResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	deploymentData, err := s.queries.GetDeploymentByID(ctx, r.DeploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	app, err := s.queries.GetAppByID(ctx, deploymentData.AppID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get app", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	deploymentResp := &deploymentv1.Deployment{
		Id:        deploymentData.ID,
		AppId:     deploymentData.AppID,
		Image:     deploymentData.Image,
		Replicas:  deploymentData.Replicas,
		Status:    parseDeploymentPhase(deploymentData.Status),
		IsCurrent: deploymentData.IsCurrent,
		CreatedBy: deploymentData.CreatedBy,
		CreatedAt: timeutil.ParsePostgresTimestamp(deploymentData.CreatedAt.Time),
		UpdatedAt: timeutil.ParsePostgresTimestamp(deploymentData.UpdatedAt.Time),
	}

	if len(deploymentData.Config) > 0 {
		configStr := string(deploymentData.Config)
		deploymentResp.Config = &configStr
	}
	deploymentResp.SchemaVersion = deploymentData.SchemaVersion.Int32

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

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "app_id", r.AppId)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	limit := r.GetLimit()
	if limit == 0 {
		limit = 50
	}
	offset := r.GetOffset()

	total, err := s.queries.CountDeploymentsForApp(ctx, r.AppId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deploymentList, err := s.queries.ListDeploymentsForApp(ctx, genDb.ListDeploymentsForAppParams{
		AppID:  r.AppId,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployment := &deploymentv1.Deployment{
			Id:        d.ID,
			AppId:     d.AppID,
			Image:     d.Image,
			Replicas:  d.Replicas,
			Status:    parseDeploymentPhase(d.Status),
			IsCurrent: d.IsCurrent,
			CreatedBy: d.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(d.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(d.UpdatedAt.Time),
		}

		if len(d.Config) > 0 {
			configStr := string(d.Config)
			deployment.Config = &configStr
		}
		deployment.SchemaVersion = d.SchemaVersion.Int32

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

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	appID, err := s.queries.GetDeploymentAppID(ctx, r.DeploymentId)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", r.DeploymentId)
		return connect.NewError(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	app, err := s.queries.GetAppByID(ctx, appID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get app", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", app.WorkspaceID, "userId", userID)
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

// allocateDeployment runs as a background goroutine that allocates Kubernetes resources
func (s *DeploymentServer) allocateDeployment(
	ctx context.Context,
	app *genDb.App,
	deployment *genDb.Deployment,
	envVars map[string]string,
) {
	slog.InfoContext(ctx, "Starting deployment allocation", "deployment_id", deployment.ID, "app_id", app.ID)

	var config map[string]any
	if len(deployment.Config) > 0 {
		if err := json.Unmarshal(deployment.Config, &config); err != nil {
			slog.ErrorContext(ctx, "Failed to parse deployment config", "deployment_id", deployment.ID, "error", err)
			s.updateDeploymentStatus(context.Background(), deployment.ID, genDb.DeploymentStatusFailed, fmt.Sprintf("Failed to parse config: %v", err))
			return
		}
	}

	ldc, err := kube.NewLocoDeploymentContext(app, deployment)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create deployment context", "deployment_id", deployment.ID, "error", err)
		s.updateDeploymentStatus(context.Background(), deployment.ID, genDb.DeploymentStatusFailed, fmt.Sprintf("Failed to create deployment context: %v", err))
		return
	}

	s.updateDeploymentStatus(context.Background(), deployment.ID, genDb.DeploymentStatusRunning, "Allocating Kubernetes resources...")

	if err := s.kubeClient.AllocateResources(ctx, ldc, envVars, nil); err != nil {
		slog.ErrorContext(ctx, "Failed to allocate Kubernetes resources", "deployment_id", deployment.ID, "error", err)
		s.updateDeploymentStatus(context.Background(), deployment.ID, genDb.DeploymentStatusFailed, fmt.Sprintf("Failed to allocate resources: %v", err))
		return
	}

	s.updateDeploymentStatus(context.Background(), deployment.ID, genDb.DeploymentStatusSucceeded, "Deployment successful")
	slog.InfoContext(ctx, "Deployment allocation completed", "deployment_id", deployment.ID)
}

// updateDeploymentStatus updates the deployment status in the database
// todo: should we move these to the app service?
// technically we are getting app logs, app status, and updating app env/scale.
func (s *DeploymentServer) updateDeploymentStatus(ctx context.Context, deploymentID int64, status genDb.DeploymentStatus, message string) {
	messageParam := pgtype.Text{String: message, Valid: message != ""}
	err := s.queries.UpdateDeploymentStatusWithMessage(ctx, genDb.UpdateDeploymentStatusWithMessageParams{
		ID:      deploymentID,
		Status:  status,
		Message: messageParam,
	})
	if err != nil {
		slog.ErrorContext(ctx, "Failed to update deployment status", "deployment_id", deploymentID, "error", err)
	}
}
