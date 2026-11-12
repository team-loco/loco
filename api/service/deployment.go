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
	"github.com/team-loco/loco/api/pkg/admission"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrDeploymentNotFound = rpcerr.NewCoded(connect.CodeNotFound, "deployment not found")
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
	spec, err := converter.DeserializeDeploymentSpec(d.Spec, resourceType)
	if err != nil {
		return nil, fmt.Errorf("decode spec of deployment %s: %w", d.ID, err)
	}
	var commit *deploymentv1.SourceCommit
	if d.CommitSha != nil {
		commit = &deploymentv1.SourceCommit{Sha: *d.CommitSha, Dirty: d.CommitDirty}
	}
	deployment := &deploymentv1.Deployment{
		Id:            d.ID.String(),
		ResourceId:    d.ResourceID.String(),
		EnvironmentId: d.EnvironmentID.String(),
		ClusterId:     d.ClusterID.String(),
		Region:        d.Region,
		Replicas:      d.Replicas,
		Status:        parseDeploymentPhase(d.Status),
		IsActive:      d.IsActive,
		CreatedAt:     timestamppb.New(d.CreatedAt),
		UpdatedAt:     timestamppb.New(d.UpdatedAt),
		StartedAt:     timestamppb.New(d.StartedAt),
		CompletedAt:   optionalTimestamp(d.CompletedAt),
		SpecVersion:   d.SpecVersion,
		Message:       d.Message,
		Spec:          spec,
		ActorType:     parseDeploymentActorType(d.ActorType),
		ActorId:       d.ActorID.String(),
		Method:        parseDeploymentMethod(d.Method),
		Commit:        commit,
	}

	return deployment, nil
}

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	db             *pgxpool.Pool
	queries        genDb.Querier
	resolver       ImageResolver
	registryHost   string
	defaults       servicedefaults.Defaults
	authz          *authz.Authorizer
	silentAfter    time.Duration
	maxSecretBytes int32
}

// NewDeploymentServer creates a new DeploymentServer instance
func NewDeploymentServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	resolver ImageResolver,
	registryHost string,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
	maxSecretBytes int32,
) *DeploymentServer {
	return &DeploymentServer{
		db:             db,
		queries:        queries,
		resolver:       resolver,
		registryHost:   registryHost,
		defaults:       defaults,
		authz:          authz.New(db, queries),
		silentAfter:    clusterSilentAfter,
		maxSecretBytes: maxSecretBytes,
	}
}

func (s *DeploymentServer) ResolveImage(
	ctx context.Context,
	req *deploymentv1.ResolveImageRequest,
) (*deploymentv1.ResolveImageResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.CreateResource, workspaceID); err != nil {
		return nil, err
	}
	source := &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: req.GetImage()}
	pinned, err := pinPublicImage(ctx, s.resolver, s.registryHost, source)
	if err != nil {
		return nil, err
	}
	return &deploymentv1.ResolveImageResponse{Image: pinned.GetImage()}, nil
}

// CreateDeployment creates a new deployment
func (s *DeploymentServer) CreateDeployment(
	ctx context.Context,
	req *deploymentv1.CreateDeploymentRequest,
) (*deploymentv1.CreateDeploymentResponse, error) {
	principal, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}

	resource, err := s.resourceForDeployment(ctx, req.GetResourceId())
	if err != nil {
		return nil, err
	}
	resourceID := resource.ID

	if admitErr := admission.AdmitWork(ctx, resource.WorkspaceID, admission.Deploy); admitErr != nil {
		return nil, admitErr
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
		return nil, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != resource.WorkspaceID {
		return nil, ErrEnvironmentNotFound
	}

	cluster, err := eligibleCluster(ctx, s.queries, region, env.EnvironmentType, s.silentAfter)
	if err != nil {
		return nil, fmt.Errorf("get active cluster: %w", err)
	}

	requestedBuild := serviceSpec.GetBuild()
	pinnedBuild, err := pinBuildSource(ctx, s.queries, s.resolver, s.registryHost, resourceID, requestedBuild)
	if err != nil {
		return nil, err
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
			return marshalErr
		}

		plainEnv, secretNames, bindingsErr := s.deploymentBindings(
			ctx,
			qtx,
			resourceID,
			environmentID,
			req,
			mergedServiceSpec.GetEnv(),
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
			ActorType:     principal.ActorType,
			ActorID:       principal.ActorID,
			Method:        principal.Method,
			CommitSha:     sourceCommitSHA(req.GetCommit()),
			CommitDirty:   req.GetCommit().GetDirty(),
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
				events.FieldResourceID:    resourceID.String(),
				events.FieldEnvironmentID: environmentID.String(),
			},
		})
	})
	if err != nil {
		return nil, err
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
		return nil, ErrDeploymentNotFound
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

	actor, err := s.queries.GetDeploymentActor(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment actor: %w", err)
	}
	deployment.ActorName = actor.Name
	deployment.ActorAvatarUrl = actor.AvatarUrl
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
		return nil, ErrResourceNotFound
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
		deployment, err := deploymentToProto(d.Deployment, resource.Type)
		if err != nil {
			return nil, err
		}
		deployment.ActorName = d.ActorName
		deployment.ActorAvatarUrl = d.ActorAvatarUrl
		deployments = append(deployments, deployment)
	}

	token := nextPageToken(
		deploymentList,
		pageSize,
		func(d genDb.ListDeploymentsForResourceRow) (uuid.UUID, time.Time) {
			return d.Deployment.ID, d.Deployment.CreatedAt
		},
	)
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
		return nil, ErrDeploymentNotFound
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
		return ErrDeploymentNotFound
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

func (s *DeploymentServer) resourceForDeployment(ctx context.Context, rawID string) (genDb.Resource, error) {
	resourceID, err := parseID("resource_id", rawID)
	if err != nil {
		return genDb.Resource{}, err
	}
	required := actions.New(actions.CreateDeployment, resourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrResourceNotFound); authErr != nil {
		return genDb.Resource{}, authErr
	}
	resource, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Resource{}, ErrResourceNotFound
	}
	if err != nil {
		return genDb.Resource{}, fmt.Errorf("get resource: %w", err)
	}
	return resource, nil
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

// optionalTimestamp converts a nullable database time, mapping nil to a nil timestamp.
func optionalTimestamp(ts *time.Time) *timestamppb.Timestamp {
	if ts == nil {
		return nil
	}
	return timestamppb.New(*ts)
}

func sourceCommitSHA(commit *deploymentv1.SourceCommit) *string {
	if commit == nil {
		return nil
	}
	return new(commit.GetSha())
}

func parseDeploymentActorType(actor genDb.DeploymentActorType) deploymentv1.DeploymentActorType {
	switch actor {
	case genDb.DeploymentActorTypeUser:
		return deploymentv1.DeploymentActorType_DEPLOYMENT_ACTOR_TYPE_USER
	case genDb.DeploymentActorTypeApiToken:
		return deploymentv1.DeploymentActorType_DEPLOYMENT_ACTOR_TYPE_API_TOKEN
	default:
		return deploymentv1.DeploymentActorType_DEPLOYMENT_ACTOR_TYPE_UNSPECIFIED
	}
}

func parseDeploymentMethod(method genDb.DeploymentMethod) deploymentv1.DeploymentMethod {
	switch method {
	case genDb.DeploymentMethodApiToken:
		return deploymentv1.DeploymentMethod_DEPLOYMENT_METHOD_API_TOKEN
	case genDb.DeploymentMethodCli:
		return deploymentv1.DeploymentMethod_DEPLOYMENT_METHOD_CLI
	case genDb.DeploymentMethodWeb:
		return deploymentv1.DeploymentMethod_DEPLOYMENT_METHOD_WEB
	default:
		return deploymentv1.DeploymentMethod_DEPLOYMENT_METHOD_UNSPECIFIED
	}
}
