package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	timeutil "github.com/team-loco/loco/api/timeutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrDeploymentNotFound = errors.New("deployment not found")
)

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

func deploymentToProto(d genDb.Deployment, resourceType genDb.ResourceType) (*deploymentv1.Deployment, error) {
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

	spec, err := converter.DeserializeDeploymentSpec(d.Spec, resourceType)
	if err != nil {
		return nil, fmt.Errorf("decode spec of deployment %s: %w", d.ID, err)
	}
	deployment.Spec = spec

	deployment.StartedAt = timeutil.ParsePostgresTimestamp(d.StartedAt)
	deployment.CompletedAt = timeutil.ParsePostgresTimestampPtr(d.CompletedAt)

	return deployment, nil
}

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db           *pgxpool.Pool
	queries      genDb.Querier
	resolver     ImageResolver
	registryHost string
	defaults     servicedefaults.Defaults
	authz        *authz.Authorizer
	silentAfter  time.Duration
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	resolver ImageResolver,
	registryHost string,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
) *DeploymentServer {
	return &DeploymentServer{
		db:           db,
		queries:      queries,
		resolver:     resolver,
		registryHost: registryHost,
		defaults:     defaults,
		authz:        authz.New(db, queries),
		silentAfter:  clusterSilentAfter,
	}
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *deploymentv1.CreateDeploymentRequest,
) (*deploymentv1.CreateDeploymentResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}

	required := actions.New(actions.CreateDeployment, resourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrResourceNotFound); authErr != nil {
		return nil, authErr
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	// validate that request spec contains a service deployment (for now, only services are supported)
	if req.GetSpec().GetService() == nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			"only service deployments are currently supported",
		)
	}

	serviceSpec := req.GetSpec().GetService()

	region := req.GetRegion()
	environmentID := uuid.MustParse(req.GetEnvironmentId())

	env, err := s.queries.GetEnvironmentByID(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != resource.WorkspaceID {
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	cluster, err := eligibleCluster(ctx, s.queries, region, env.EnvironmentType, s.silentAfter)
	if err != nil {
		return nil, mapError("get active cluster", err)
	}

	requestedBuild := serviceSpec.GetBuild()
	pinnedBuild, err := pinBuildSource(ctx, s.queries, s.resolver, s.registryHost, resourceID, requestedBuild)
	if err != nil {
		return nil, mapError("pin the build source", err)
	}
	originalSpec := req.GetSpec()
	requestSpec := proto.CloneOf(originalSpec)
	requestSpec.GetService().Build = pinnedBuild

	organizationID, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, resource.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("get workspace organization: %w", err)
	}

	var deploymentID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockOrganization(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if rolloutErr := ensureNoRollout(ctx, qtx, resourceID, environmentID, region); rolloutErr != nil {
			return rolloutErr
		}
		if lockErr := lockPinnedBuildImage(ctx, qtx, pinnedBuild); lockErr != nil {
			return lockErr
		}
		resource, getErr := qtx.GetResourceByID(ctx, resourceID)
		if getErr != nil {
			return fmt.Errorf("read resource before deployment: %w", getErr)
		}
		hostname, hostnameErr := primaryHostname(ctx, qtx, resourceID, environmentID)
		if hostnameErr != nil {
			return hostnameErr
		}

		resourceSpec, deserializeErr := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
		if deserializeErr != nil {
			return fmt.Errorf("%w: decode resource spec: %w", errDesiredSpec, deserializeErr)
		}

		mergedSpec, mergeErr := converter.MergeDeploymentSpec(resourceSpec, requestSpec, region, s.defaults)
		if errors.Is(mergeErr, converter.ErrRegionNotFound) || errors.Is(mergeErr, converter.ErrRegionDisabled) {
			return &invalidInputError{public: publicInvalidRequest, err: mergeErr}
		}
		if mergeErr != nil {
			return fmt.Errorf("%w: merge deployment spec: %w", errDesiredSpec, mergeErr)
		}

		mergedServiceSpec := mergedSpec.GetService()
		if quantityErr := validateQuantities(mergedServiceSpec); quantityErr != nil {
			return &invalidInputError{public: publicInvalidRequest, err: quantityErr}
		}
		replicas := mergedServiceSpec.GetMinReplicas()

		specJSON, marshalErr := deploymentSpecJSON(mergedServiceSpec)
		if marshalErr != nil {
			slog.ErrorContext(ctx, "failed to marshal spec", "error", marshalErr)
			return &invalidInputError{public: publicInvalidSpec, err: marshalErr}
		}

		requestedEnv := mergedServiceSpec.GetEnv()
		plainEnv, secretNames, bindingsErr := currentDeploymentBindings(
			ctx,
			qtx,
			resourceID,
			environmentID,
			region,
			requestedEnv,
		)
		if bindingsErr != nil {
			return bindingsErr
		}
		mergedServiceSpec.Env = plainEnv
		buildSpec := desiredApplicationSpec(
			resource,
			organizationID,
			resourceSpec,
			hostname,
			mergedSpec,
			region,
			environmentID,
			env.Name,
			s.defaults,
		)

		var txErr error
		deploymentID, txErr = createDeploymentWithCleanup(ctx, qtx, genDb.CreateDeploymentParams{
			ResourceID:    resourceID,
			ClusterID:     cluster.ID,
			Region:        region,
			Replicas:      replicas,
			Status:        genDb.DeploymentStatusPending,
			IsActive:      true,
			Message:       "Scheduling deployment",
			Spec:          specJSON,
			SpecVersion:   specVersion,
			EnvironmentID: environmentID,
			SecretNames:   secretNames,
		}, buildSpec)
		if txErr != nil {
			return txErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentCreated,
			WorkspaceID: new(resource.WorkspaceID),
			ResourceID:  new(resource.ID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deploymentID),
			Data: map[string]any{
				events.FieldResourceID: resourceID.String(),
				"environmentId":        environmentID.String(),
			},
		})
	})
	if err != nil {
		return nil, deploymentTxError(ctx, err)
	}

	return &deploymentv1.CreateDeploymentResponse{
		DeploymentId: deploymentID.String(),
		Build:        pinnedBuild,
	}, nil
}

// GetDeployment retrieves a deployment by ID
func (s *DeploymentServer) GetDeployment(
	ctx context.Context,
	req *deploymentv1.GetDeploymentRequest,
) (*deploymentv1.GetDeploymentResponse, error) {
	deploymentID := uuid.MustParse(req.GetDeploymentId())

	deploymentData, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}

	required := actions.New(actions.GetDeployment, deploymentData.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return nil, authErr
	}

	resource, err := s.queries.GetResourceByID(ctx, deploymentData.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	deployment, err := deploymentToProto(deploymentData, resource.Type)
	if err != nil {
		return nil, err
	}

	return &deploymentv1.GetDeploymentResponse{
		Deployment: deployment,
	}, nil
}

// ListDeployments lists deployments for a resource
func (s *DeploymentServer) ListDeployments(
	ctx context.Context,
	req *deploymentv1.ListDeploymentsRequest,
) (*deploymentv1.ListDeploymentsResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListDeployments, resourceID); err != nil {
		return nil, err
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	var environmentID *uuid.UUID
	if req.GetEnvironmentId() != "" {
		id := uuid.MustParse(req.GetEnvironmentId())
		environmentID = &id
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID:    resourceID,
		Limit:         pageSize,
		EnvironmentID: environmentID,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployment, err := deploymentToProto(d, resource.Type)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, deployment)
	}

	token := nextPageToken(deploymentList, pageSize, func(d genDb.Deployment) (uuid.UUID, time.Time) {
		return d.ID, d.CreatedAt
	})
	return &deploymentv1.ListDeploymentsResponse{
		Deployments:   deployments,
		NextPageToken: token,
	}, nil
}

// DeleteDeployment takes a deployment out of service and removes its Application.
func (s *DeploymentServer) DeleteDeployment(
	ctx context.Context,
	req *deploymentv1.DeleteDeploymentRequest,
) (*deploymentv1.DeleteDeploymentResponse, error) {
	deploymentID := uuid.MustParse(req.GetDeploymentId())

	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}

	required := actions.New(actions.DeleteDeployment, deployment.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return nil, authErr
	}

	resource, err := s.queries.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		current, getErr := qtx.GetDeploymentByID(ctx, deploymentID)
		if getErr != nil {
			return fmt.Errorf("read deployment before deletion: %w", getErr)
		}
		if retireErr := retireDeployment(ctx, qtx, resource, current); retireErr != nil {
			return retireErr
		}
		return bumpEnvironmentRevision(ctx, qtx, deployment.EnvironmentID)
	})
	if err != nil {
		return nil, fmt.Errorf("delete deployment: %w", err)
	}

	return &deploymentv1.DeleteDeploymentResponse{}, nil
}

const (
	watchDeploymentPollInterval = 2 * time.Second
	watchDeploymentMaxDuration  = 30 * time.Minute
)

// WatchDeployment streams deployment status updates
func (s *DeploymentServer) WatchDeployment(
	ctx context.Context,
	req *deploymentv1.WatchDeploymentRequest,
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
) error {
	deploymentID := uuid.MustParse(req.GetDeploymentId())

	resourceID, err := s.queries.GetDeploymentResourceID(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}
	if err != nil {
		return fmt.Errorf("get deployment resource ID: %w", err)
	}

	required := actions.New(actions.StreamDeployment, resourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return authErr
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
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
	deploymentID uuid.UUID,
	lastStatus genDb.DeploymentStatus,
) (genDb.DeploymentStatus, error) {
	deployment, err := s.queries.GetDeploymentStatus(ctx, deploymentID)
	if err != nil {
		return lastStatus, fmt.Errorf("get deployment status: %w", err)
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

// deploymentRouting is the routing an Application uses: the deployment's own when it carries
// one, so each environment keeps its routing, and the resource's otherwise.
func deploymentRouting(
	resourceService *resourcev1.ServiceSpec,
	deploymentService *deploymentv1.ServiceDeploymentSpec,
) *resourcev1.RoutingConfig {
	if deploymentService.GetRouting() == nil {
		return resourceService.GetRouting()
	}
	return &resourcev1.RoutingConfig{
		PathPrefix:  deploymentService.GetRouting().GetPathPrefix(),
		IdleTimeout: deploymentService.GetRouting().GetIdleTimeout(),
	}
}

func validateQuantities(service *deploymentv1.ServiceDeploymentSpec) error {
	if _, err := servicedefaults.ParseCPU(service.GetCpu()); err != nil {
		return err
	}
	if _, err := servicedefaults.ParseMemory(service.GetMemory()); err != nil {
		return err
	}
	return nil
}
