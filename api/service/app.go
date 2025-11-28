package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/klogmux"
	"github.com/loco-team/loco/api/pkg/kube"
	"github.com/loco-team/loco/api/timeutil"
	appv1 "github.com/loco-team/loco/shared/proto/app/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var (
	ErrAppNotFound           = errors.New("app not found")
	ErrAppNameNotUnique      = errors.New("app name already exists in this workspace")
	ErrSubdomainNotAvailable = errors.New("subdomain already in use")
	ErrClusterNotFound       = errors.New("cluster not found")
	ErrClusterNotHealthy     = errors.New("cluster is not healthy")
	ErrInvalidAppType        = errors.New("invalid app type")
)

type AppServer struct {
	db         *pgxpool.Pool
	queries    *genDb.Queries
	kubeClient *kube.Client
}

// NewAppServer creates a new AppServer instance
func NewAppServer(db *pgxpool.Pool, queries *genDb.Queries, kubeClient *kube.Client) *AppServer {
	// todo: move this out.
	return &AppServer{
		db:         db,
		queries:    queries,
		kubeClient: kubeClient,
	}
}

// CreateApp creates a new app
func (s *AppServer) CreateApp(
	ctx context.Context,
	req *connect.Request[appv1.CreateAppRequest],
) (*connect.Response[appv1.CreateAppResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// todo: revisit validating roles
	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != "admin" && role != "deploy" {
		slog.WarnContext(ctx, "user does not have permission to create app", "workspaceId", r.WorkspaceId, "userId", userID, "role", role)
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin or have deploy role"))
	}

	domain := r.GetDomain()
	if domain == "" {
		domain = "loco.deploy-app.com"
	}

	available, err := s.queries.CheckSubdomainAvailability(ctx, genDb.CheckSubdomainAvailabilityParams{
		Subdomain: r.Subdomain,
		Domain:    domain,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check subdomain availability", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !available {
		slog.WarnContext(ctx, "subdomain not available", "subdomain", r.Subdomain, "domain", domain)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrSubdomainNotAvailable)
	}

	// todo: Get cluster details and validate health
	// clusterDetails, err := s.queries.GetClusterDetails(ctx, r.ClusterId)
	// if err != nil {
	// 	slog.WarnContext(ctx, "cluster not found", "cluster_id", r.ClusterId)
	// 	return nil, connect.NewError(connect.CodeNotFound, ErrClusterNotFound)
	// }

	// if !clusterDetails.IsActive.Bool || clusterDetails.HealthStatus.String != "healthy" {
	// 	slog.WarnContext(ctx, "cluster is not healthy or active", "cluster_id", r.ClusterId, "is_active", clusterDetails.IsActive.Bool, "health_status", clusterDetails.HealthStatus.String)
	// 	return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterNotHealthy)
	// }

	// todo: set namepsace after creating and saving app. or perhaps its set after first deployment on the app.
	app, err := s.queries.CreateApp(ctx, genDb.CreateAppParams{
		WorkspaceID: r.WorkspaceId,
		ClusterID:   1,
		Name:        r.Name,
		Type:        int32(r.Type.Number()),
		Subdomain:   r.Subdomain,
		Domain:      domain,
		CreatedBy:   userID,
		// ns empty until first deployment occurs on the app.
		// Namespace:   ns,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create app", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&appv1.CreateAppResponse{
		App:     dbAppToProto(app),
		Message: "App created successfully",
	}), nil
}

// GetApp retrieves an app by ID
func (s *AppServer) GetApp(
	ctx context.Context,
	req *connect.Request[appv1.GetAppRequest],
) (*connect.Response[appv1.GetAppResponse], error) {
	r := req.Msg

	// todo: role checks should actually be done first.
	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "id", r.AppId)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of app's workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	return connect.NewResponse(&appv1.GetAppResponse{
		App: dbAppToProto(app),
	}), nil
}

// GetAppByName retrieves an app by workspace and name
func (s *AppServer) GetAppByName(
	ctx context.Context,
	req *connect.Request[appv1.GetAppByNameRequest],
) (*connect.Response[appv1.GetAppByNameResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	_, err := s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	app, err := s.queries.GetAppByNameAndWorkspace(ctx, genDb.GetAppByNameAndWorkspaceParams{
		WorkspaceID: r.WorkspaceId,
		Name:        r.Name,
	})
	if err != nil {
		slog.WarnContext(ctx, "app not found", "workspaceId", r.WorkspaceId, "app_name", r.Name)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}

	return connect.NewResponse(&appv1.GetAppByNameResponse{
		App: dbAppToProto(app),
	}), nil
}

// ListApps lists all apps in a workspace
func (s *AppServer) ListApps(
	ctx context.Context,
	req *connect.Request[appv1.ListAppsRequest],
) (*connect.Response[appv1.ListAppsResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	_, err := s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	dbApps, err := s.queries.ListAppsForWorkspace(ctx, r.WorkspaceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list apps", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var apps []*appv1.App
	for _, dbApp := range dbApps {
		apps = append(apps, dbAppToProto(dbApp))
	}

	return connect.NewResponse(&appv1.ListAppsResponse{
		Apps: apps,
	}), nil
}

// UpdateApp updates an app
func (s *AppServer) UpdateApp(
	ctx context.Context,
	req *connect.Request[appv1.UpdateAppRequest],
) (*connect.Response[appv1.UpdateAppResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID, err := s.queries.GetAppWorkspaceID(ctx, r.AppId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", fmt.Sprintf("%d", workspaceID), "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin && role != genDb.WorkspaceRoleDeploy {
		slog.WarnContext(ctx, "user does not have permission to update app", "workspaceId", fmt.Sprintf("%d", workspaceID), "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin or deploy role to update app"))
	}

	updateParams := genDb.UpdateAppParams{
		ID: r.AppId,
	}

	if r.GetName() != "" {
		updateParams.Name = pgtype.Text{String: r.GetName(), Valid: true}
	}

	if r.GetSubdomain() != "" {
		updateParams.Subdomain = pgtype.Text{String: r.GetSubdomain(), Valid: true}
	}

	if r.GetDomain() != "" {
		updateParams.Domain = pgtype.Text{String: r.GetDomain(), Valid: true}
	}

	app, err := s.queries.UpdateApp(ctx, updateParams)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update app", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&appv1.UpdateAppResponse{
		App:     dbAppToProto(app),
		Message: "App updated successfully",
	}), nil
}

// DeleteApp deletes an app
func (s *AppServer) DeleteApp(
	ctx context.Context,
	req *connect.Request[appv1.DeleteAppRequest],
) (*connect.Response[appv1.DeleteAppResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID, err := s.queries.GetAppWorkspaceID(ctx, r.AppId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", fmt.Sprintf("%d", workspaceID), "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of workspace", "workspaceId", fmt.Sprintf("%d", workspaceID), "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin to delete app"))
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get app", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	err = s.queries.DeleteApp(ctx, r.AppId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete app", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&appv1.DeleteAppResponse{
		App:     dbAppToProto(app),
		Message: "App deleted successfully",
	}), nil
}

// CheckSubdomainAvailability checks if a subdomain is available
func (s *AppServer) CheckSubdomainAvailability(
	ctx context.Context,
	req *connect.Request[appv1.CheckSubdomainAvailabilityRequest],
) (*connect.Response[appv1.CheckSubdomainAvailabilityResponse], error) {
	r := req.Msg

	available, err := s.queries.CheckSubdomainAvailability(ctx, genDb.CheckSubdomainAvailabilityParams{
		Subdomain: r.Subdomain,
		Domain:    r.Domain,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check subdomain availability", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&appv1.CheckSubdomainAvailabilityResponse{
		Available: available,
	}), nil
}

// GetAppStatus retrieves an app and its current deployment status
func (s *AppServer) GetAppStatus(
	ctx context.Context,
	req *connect.Request[appv1.GetAppStatusRequest],
) (*connect.Response[appv1.GetAppStatusResponse], error) {
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

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of app's workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	deploymentList, err := s.queries.ListDeploymentsForApp(ctx, genDb.ListDeploymentsForAppParams{
		AppID:  r.AppId,
		Limit:  1,
		Offset: 0,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var deploymentStatus *appv1.DeploymentStatus
	if len(deploymentList) > 0 {
		deployment := deploymentList[0]
		deploymentStatus = &appv1.DeploymentStatus{
			Id:       deployment.ID,
			Status:   deploymentStatusToProto(deployment.Status),
			Replicas: deployment.Replicas,
		}
		if deployment.Message.Valid {
			deploymentStatus.Message = &deployment.Message.String
		}
		if deployment.ErrorMessage.Valid {
			deploymentStatus.ErrorMessage = &deployment.ErrorMessage.String
		}
	}

	return connect.NewResponse(&appv1.GetAppStatusResponse{
		App:               dbAppToProto(app),
		CurrentDeployment: deploymentStatus,
	}), nil
}

// StreamLogs streams logs for an app
func (s *AppServer) StreamLogs(
	ctx context.Context,
	req *connect.Request[appv1.StreamLogsRequest],
	stream *connect.ServerStream[appv1.LogEntry],
) error {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "app_id", r.AppId)
		return connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of app's workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if app.Namespace == "" {
		slog.WarnContext(ctx, "app has no namespace assigned", "app_id", r.AppId)
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("app has not been deployed yet"))
	}

	slog.InfoContext(ctx, "streaming logs for app", "app_id", r.AppId, "app_namespace", app.Namespace)

	// build label selector to find pods for this app
	selector := labels.SelectorFromSet(labels.Set{"app": app.Name})

	// build the log stream
	builder := klogmux.NewBuilder(s.kubeClient.ClientSet).
		Namespace(app.Namespace).
		LabelSelector(selector.String()).
		Follow(r.GetFollow())

	if r.Limit != nil {
		builder.TailLines(int64(*r.Limit))
	}

	logStream := builder.Build()

	// start the log stream
	logStream.Start(ctx)
	defer logStream.Stop()

	slog.DebugContext(ctx, "log stream started", "app_id", r.AppId, "namespace", app.Namespace)

	// stream log entries to client
	for entry := range logStream.Entries() {
		logProto := &appv1.LogEntry{
			PodName:   entry.PodName,
			Namespace: entry.Namespace,
			Container: entry.Container,
			Timestamp: timestamppb.New(entry.Timestamp),
			Log:       entry.Message,
		}

		if entry.IsError {
			logProto.Level = "ERROR"
		} else {
			logProto.Level = "INFO"
		}

		if err := stream.Send(logProto); err != nil {
			slog.ErrorContext(ctx, "failed to send log entry", "error", err)
			return err
		}
	}

	// check for stream errors
	for err := range logStream.Errors() {
		if err != nil {
			slog.ErrorContext(ctx, "log stream error", "error", err)
			return connect.NewError(connect.CodeInternal, fmt.Errorf("log stream error: %w", err))
		}
	}

	slog.DebugContext(ctx, "log stream completed", "app_id", r.AppId)
	return nil
}

// GetEvents retrieves Kubernetes events for an app
func (s *AppServer) GetEvents(
	ctx context.Context,
	req *connect.Request[appv1.GetEventsRequest],
) (*connect.Response[appv1.GetEventsResponse], error) {
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

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: app.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of app's workspace", "workspaceId", app.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if app.Namespace == "" {
		slog.WarnContext(ctx, "app has no namespace assigned", "app_id", r.AppId)
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("app has not been deployed yet"))
	}

	slog.InfoContext(ctx, "fetching events for app", "app_id", r.AppId, "app_namespace", app.Namespace)

	eventList, err := s.kubeClient.ClientSet.CoreV1().Events(app.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list events from kubernetes", "error", err, "namespace", app.Namespace)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to fetch events: %w", err))
	}

	var protoEvents []*appv1.Event
	for _, k8sEvent := range eventList.Items {
		// filter events to those related to this app's pods
		if k8sEvent.InvolvedObject.Kind != "Pod" {
			continue
		}

		protoEvent := &appv1.Event{
			Timestamp: timestamppb.New(k8sEvent.FirstTimestamp.Time),
			Reason:    k8sEvent.Reason,
			Message:   k8sEvent.Message,
			Type:      k8sEvent.Type,
			PodName:   k8sEvent.InvolvedObject.Name,
		}
		protoEvents = append(protoEvents, protoEvent)
	}

	// sort by timestamp descending (newest first)
	sort.Slice(protoEvents, func(i, j int) bool {
		return protoEvents[i].Timestamp.AsTime().After(protoEvents[j].Timestamp.AsTime())
	})

	// apply limit if specified
	if r.Limit != nil && *r.Limit > 0 && int(*r.Limit) < len(protoEvents) {
		protoEvents = protoEvents[:*r.Limit]
	}

	slog.DebugContext(ctx, "fetched events for app", "app_id", r.AppId, "event_count", len(protoEvents))

	return connect.NewResponse(&appv1.GetEventsResponse{
		Events: protoEvents,
	}), nil
}

// ScaleApp scales an application by creating a new deployment with updated resources
func (s *AppServer) ScaleApp(
	ctx context.Context,
	req *connect.Request[appv1.ScaleAppRequest],
) (*connect.Response[appv1.ScaleAppResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if r.Replicas == nil && r.Cpu == nil && r.Memory == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one of replicas, cpu, or memory must be provided"))
	}

	if r.Replicas != nil && *r.Replicas < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidReplicas)
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "app_id", r.AppId)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}
	workspaceID := app.WorkspaceID

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

	deploymentList, err := s.queries.ListDeploymentsForApp(ctx, genDb.ListDeploymentsForAppParams{
		AppID:  r.AppId,
		Limit:  1,
		Offset: 0,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if len(deploymentList) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no existing deployment found for app"))
	}

	currentDeployment := deploymentList[0]

	var config map[string]any
	if len(currentDeployment.Config) > 0 {
		if err := json.Unmarshal(currentDeployment.Config, &config); err != nil {
			slog.ErrorContext(ctx, "failed to parse deployment config", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid config: %w", err))
		}
	} else {
		config = make(map[string]any)
	}

	var env map[string]string
	if envData, ok := config["env"].(map[string]any); ok {
		env = make(map[string]string)
		for k, v := range envData {
			env[k] = v.(string)
		}
	} else {
		env = make(map[string]string)
	}

	var cpu, memory *string
	if resourceData, ok := config["resources"].(map[string]any); ok {
		if cpuVal, ok := resourceData["cpu"].(string); ok && cpuVal != "" {
			cpu = &cpuVal
		}
		if memoryVal, ok := resourceData["memory"].(string); ok && memoryVal != "" {
			memory = &memoryVal
		}
	}

	var ports []any
	if portData, ok := config["ports"].([]any); ok {
		ports = portData
	}

	replicas := currentDeployment.Replicas
	if r.Replicas != nil {
		replicas = *r.Replicas
	}

	if r.Cpu != nil {
		cpu = r.Cpu
	}

	if r.Memory != nil {
		memory = r.Memory
	}

	resources := map[string]any{}
	if cpu != nil {
		resources["cpu"] = *cpu
	}
	if memory != nil {
		resources["memory"] = *memory
	}

	updatedConfig := map[string]any{
		"env":       env,
		"ports":     ports,
		"resources": resources,
	}
	configJSON, err := json.Marshal(updatedConfig)
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
		Image:         currentDeployment.Image,
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

	go s.allocateDeployment(context.Background(), &app, &deployment, env)

	deploymentStatus := &appv1.DeploymentStatus{
		Id:       deployment.ID,
		Status:   deploymentStatusToProto(deployment.Status),
		Replicas: deployment.Replicas,
	}

	if deployment.Message.Valid {
		deploymentStatus.Message = &deployment.Message.String
	}

	if deployment.ErrorMessage.Valid {
		deploymentStatus.ErrorMessage = &deployment.ErrorMessage.String
	}

	return connect.NewResponse(&appv1.ScaleAppResponse{
		Deployment: deploymentStatus,
	}), nil
}

// UpdateAppEnv updates environment variables for an application
func (s *AppServer) UpdateAppEnv(
	ctx context.Context,
	req *connect.Request[appv1.UpdateAppEnvRequest],
) (*connect.Response[appv1.UpdateAppEnvResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if len(r.Env) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one environment variable must be provided"))
	}

	app, err := s.queries.GetAppByID(ctx, r.AppId)
	if err != nil {
		slog.WarnContext(ctx, "app not found", "app_id", r.AppId)
		return nil, connect.NewError(connect.CodeNotFound, ErrAppNotFound)
	}
	workspaceID := app.WorkspaceID

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

	deploymentList, err := s.queries.ListDeploymentsForApp(ctx, genDb.ListDeploymentsForAppParams{
		AppID:  r.AppId,
		Limit:  1,
		Offset: 0,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if len(deploymentList) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no existing deployment found for app"))
	}

	currentDeployment := deploymentList[0]

	var config map[string]any
	if len(currentDeployment.Config) > 0 {
		if err := json.Unmarshal(currentDeployment.Config, &config); err != nil {
			slog.ErrorContext(ctx, "failed to parse deployment config", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid config: %w", err))
		}
	} else {
		config = make(map[string]any)
	}

	var cpu, memory *string
	if resourceData, ok := config["resources"].(map[string]any); ok {
		if cpuVal, ok := resourceData["cpu"].(string); ok && cpuVal != "" {
			cpu = &cpuVal
		}
		if memoryVal, ok := resourceData["memory"].(string); ok && memoryVal != "" {
			memory = &memoryVal
		}
	}

	var ports []any
	if portData, ok := config["ports"].([]any); ok {
		ports = portData
	}

	resources := map[string]any{}
	if cpu != nil {
		resources["cpu"] = *cpu
	}
	if memory != nil {
		resources["memory"] = *memory
	}

	updatedConfig := map[string]any{
		"env":       r.Env,
		"ports":     ports,
		"resources": resources,
	}
	configJSON, err := json.Marshal(updatedConfig)
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
		Image:         currentDeployment.Image,
		Replicas:      currentDeployment.Replicas,
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

	deploymentStatus := &appv1.DeploymentStatus{
		Id:       deployment.ID,
		Status:   deploymentStatusToProto(deployment.Status),
		Replicas: deployment.Replicas,
	}

	if deployment.Message.Valid {
		deploymentStatus.Message = &deployment.Message.String
	}

	if deployment.ErrorMessage.Valid {
		deploymentStatus.ErrorMessage = &deployment.ErrorMessage.String
	}

	return connect.NewResponse(&appv1.UpdateAppEnvResponse{
		Deployment: deploymentStatus,
	}), nil
}

// allocateDeployment runs as a background goroutine that allocates Kubernetes resources
func (s *AppServer) allocateDeployment(
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
func (s *AppServer) updateDeploymentStatus(ctx context.Context, deploymentID int64, status genDb.DeploymentStatus, message string) {
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

// deploymentStatusToProto converts database deployment status to proto enum
func deploymentStatusToProto(status genDb.DeploymentStatus) appv1.DeploymentPhase {
	switch status {
	case genDb.DeploymentStatusPending:
		return appv1.DeploymentPhase_PENDING
	case genDb.DeploymentStatusRunning:
		return appv1.DeploymentPhase_RUNNING
	case genDb.DeploymentStatusSucceeded:
		return appv1.DeploymentPhase_SUCCEEDED
	case genDb.DeploymentStatusFailed:
		return appv1.DeploymentPhase_FAILED
	default:
		return appv1.DeploymentPhase_PENDING
	}
}

// dbAppToProto converts a database App to the proto App
// to be returned to client.
func dbAppToProto(app genDb.App) *appv1.App {
	appType := appv1.AppType(app.Type)
	return &appv1.App{
		Id:          app.ID,
		WorkspaceId: app.WorkspaceID,
		Name:        app.Name,
		Namespace:   app.Namespace,
		Type:        appType,
		Subdomain:   app.Subdomain,
		Domain:      app.Domain,
		CreatedBy:   app.CreatedBy,
		CreatedAt:   timeutil.ParsePostgresTimestamp(app.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(app.UpdatedAt.Time),
	}
}
