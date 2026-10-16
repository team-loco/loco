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
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	timeutil "github.com/team-loco/loco/api/timeutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var (
	errResourceSpecNotService = errors.New("resource spec missing service configuration")
	errDatabaseNotImplemented = errors.New("database resource type not yet implemented")
	errCacheNotImplemented    = errors.New("cache resource type not yet implemented")
	errQueueNotImplemented    = errors.New("queue resource type not yet implemented")
	errBlobNotImplemented     = errors.New("blob resource type not yet implemented")
	ErrDeploymentNotFound     = errors.New("deployment not found")
)

type ApplicationPayload struct {
	DeploymentID string                            `json:"deployment_id"`
	ResourceID   string                            `json:"resource_id"`
	WorkspaceID  string                            `json:"workspace_id"`
	ResourceName string                            `json:"resource_name"`
	ResourceType string                            `json:"resource_type"`
	Region       string                            `json:"region"`
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
	resourceID := uuid.MustParse(req.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", req.GetResourceId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateDeployment, req.GetResourceId()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to create deployment", "resourceId", req.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
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
		slog.WarnContext(ctx, "environment not found", "environmentId", environmentID)
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", environmentID)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	cluster, err := eligibleCluster(ctx, s.queries, region, env.EnvironmentType, s.silentAfter)
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
		return nil, rpcerr.New(connect.CodeFailedPrecondition, err)
	}

	requestedBuild := serviceSpec.GetBuild()
	pinnedBuild, err := pinBuildSource(ctx, s.queries, s.resolver, s.registryHost, resourceID, requestedBuild)
	if err != nil {
		return nil, err
	}
	originalSpec := req.GetSpec()
	clonedSpec := proto.Clone(originalSpec)
	requestSpec, ok := clonedSpec.(*deploymentv1.DeploymentSpec)
	if !ok {
		slog.ErrorContext(ctx, "failed to clone deployment spec")
		return nil, rpcerr.New(connect.CodeInternal, errCloneDeploymentSpec)
	}
	requestSpec.GetService().Build = pinnedBuild

	organizationID, err := s.queries.GetOrganizationIDByWorkspaceID(ctx, resource.WorkspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get workspace organization", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var deploymentID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
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
			slog.ErrorContext(
				ctx,
				"failed to get the resource's primary domain",
				"resourceId",
				resourceID,
				"error",
				hostnameErr,
			)
			return rpcerr.New(connect.CodeInternal, ErrDB)
		}

		// deserialize resource spec and merge with request spec
		resourceSpec, deserializeErr := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
		if deserializeErr != nil {
			slog.ErrorContext(ctx, deserializeErr.Error())
			return connect.NewError(
				connect.CodeInternal,
				publicRequestFailed,
			).WithCause(deserializeErr)
		}

		mergedSpec, mergeErr := converter.MergeDeploymentSpec(resourceSpec, requestSpec, region, s.defaults)
		if errors.Is(mergeErr, converter.ErrRegionNotFound) || errors.Is(mergeErr, converter.ErrRegionDisabled) {
			slog.WarnContext(ctx, "deployment targets a region the resource does not run in", "error", mergeErr)
			return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(mergeErr)
		}
		if mergeErr != nil {
			slog.ErrorContext(ctx, mergeErr.Error())
			return connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(mergeErr)
		}

		// create spec copy without env for DB persistence (no plaintext secrets in DB)
		mergedServiceSpec := mergedSpec.GetService()
		if quantityErr := validateQuantities(mergedServiceSpec); quantityErr != nil {
			slog.WarnContext(ctx, "invalid resource quantity", "error", quantityErr)
			return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(quantityErr)
		}
		replicas := mergedServiceSpec.GetMinReplicas()

		clonedServiceSpec := proto.Clone(mergedServiceSpec)
		specForDBService, ok := clonedServiceSpec.(*deploymentv1.ServiceDeploymentSpec)
		if !ok {
			slog.ErrorContext(ctx, "failed to clone service spec")
			return rpcerr.New(connect.CodeInternal, errCloneServiceSpec)
		}
		specForDBService.Env = nil

		specJSON, marshalErr := protojson.Marshal(specForDBService)
		if marshalErr != nil {
			slog.ErrorContext(ctx, "failed to marshal spec", "error", marshalErr)
			return connect.NewError(connect.CodeInvalidArgument, publicInvalidSpec).WithCause(marshalErr)
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
			SpecVersion:   int32(1),
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
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", req.GetDeploymentId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deploymentData.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	// check if user has permission to get deployment (resource:read)
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetDeployment, resource.ID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get deployment", "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	return &deploymentv1.GetDeploymentResponse{
		Deployment: deploymentToProto(deploymentData, string(resource.Type)),
	}, nil
}

// ListDeployments lists deployments for a resource
func (s *DeploymentServer) ListDeployments(
	ctx context.Context,
	req *deploymentv1.ListDeploymentsRequest,
) (*deploymentv1.ListDeploymentsResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	// check if requester has permission to list deployments (resource:read)
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListDeployments, req.GetResourceId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	resourceID := uuid.MustParse(req.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", req.GetResourceId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}

	pageSize := normalizePageSize(req.GetPageSize())

	var pageToken *string
	if req.GetPageToken() != "" {
		cursorID, decodeErr := decodeCursor(req.GetPageToken())
		if decodeErr != nil {
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				publicInvalidPageToken,
			).WithCause(decodeErr)
		}
		pageToken = &cursorID
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
		PageToken:     pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var deployments []*deploymentv1.Deployment
	for _, d := range deploymentList {
		deployments = append(deployments, deploymentToProto(d, string(resource.Type)))
	}

	var nextPageToken string
	if len(deploymentList) == int(pageSize) {
		nextPageToken = encodeCursor(deploymentList[len(deploymentList)-1].ID.String())
	}

	return &deploymentv1.ListDeploymentsResponse{
		Deployments:   deployments,
		NextPageToken: nextPageToken,
	}, nil
}

// DeleteDeployment deletes/inactivates a deployment and cleans up its Application
func (s *DeploymentServer) DeleteDeployment(
	ctx context.Context,
	req *deploymentv1.DeleteDeploymentRequest,
) (*deploymentv1.DeleteDeploymentResponse, error) {
	deploymentID := uuid.MustParse(req.GetDeploymentId())

	deployment, err := s.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", req.GetDeploymentId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	resource, err := s.queries.GetResourceByID(ctx, deployment.ResourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteDeployment, resource.ID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to delete deployment", "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if deployment.IsActive {
			if removeErr := removePlacement(ctx, qtx, resource.ID, deployment.ClusterID); removeErr != nil {
				return removeErr
			}
		}

		if markErr := qtx.MarkDeploymentNotActive(ctx, deploymentID); markErr != nil {
			return fmt.Errorf("mark deployment not active: %w", markErr)
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, deployment.EnvironmentID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.DeploymentDeleted,
			WorkspaceID: new(resource.WorkspaceID),
			SubjectType: events.SubjectDeployment,
			SubjectID:   new(deploymentID),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete deployment", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
	if err != nil {
		slog.WarnContext(ctx, "deployment not found", "deployment_id", req.GetDeploymentId())
		return rpcerr.New(connect.CodeNotFound, ErrDeploymentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	resourceIDStr := resourceID.String()
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.StreamDeployment, resourceIDStr),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to stream deployment", "resourceId", resourceIDStr)
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
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
		slog.ErrorContext(ctx, "failed to get deployment status", "error", err)
		return lastStatus, rpcerr.New(connect.CodeInternal, ErrDB)
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

type invalidSpecError struct {
	err error
}

func (e *invalidSpecError) Error() string {
	return "invalid deployment spec: " + e.err.Error()
}

func (e *invalidSpecError) Unwrap() error {
	return e.err
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
		ResourceName:    resource.Name,
		OrganizationID:  organizationID.String(),
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
		return nil, &invalidSpecError{err: err}
	}

	return appSpec, nil
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
