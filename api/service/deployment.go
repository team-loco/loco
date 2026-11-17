package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

// MaxDeploymentWatchStreams caps concurrent streams per principal on an API replica.
const MaxDeploymentWatchStreams = 25

var (
	errDeploymentWatchLimit = rpcerr.NewCoded(connect.CodeResourceExhausted, "deployment watch stream limit reached")
	ErrDeploymentNotFound   = rpcerr.NewCoded(connect.CodeNotFound, "deployment not found")
)

func parseDeploymentStatus(status genDb.DeploymentStatus) deploymentv1.DeploymentStatus {
	switch status {
	case genDb.DeploymentStatusBuilding:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_BUILDING
	case genDb.DeploymentStatusDeploying:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING
	case genDb.DeploymentStatusRunning:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING
	case genDb.DeploymentStatusSuperseded:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED
	case genDb.DeploymentStatusFailed:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED
	case genDb.DeploymentStatusCanceled:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED
	default:
		return deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_UNSPECIFIED
	}
}

func deploymentToProto(row genDb.GetDeploymentViewRow) (*deploymentv1.Deployment, error) {
	d := row.Deployment
	spec, err := converter.DeserializeDeploymentSpec(d.Spec, row.ResourceType)
	if err != nil {
		return nil, fmt.Errorf("decode spec of deployment %s: %w", d.ID, err)
	}
	var commit *deploymentv1.SourceCommit
	if d.CommitSha != nil {
		commit = &deploymentv1.SourceCommit{Sha: *d.CommitSha, Dirty: d.CommitDirty}
	}
	deployment := &deploymentv1.Deployment{
		Id:              d.ID.String(),
		ResourceId:      d.ResourceID.String(),
		EnvironmentId:   d.EnvironmentID.String(),
		ClusterId:       d.ClusterID.String(),
		Region:          d.Region,
		DesiredReplicas: d.Replicas,
		Status:          parseDeploymentStatus(d.Status),
		IsActive:        d.IsActive,
		CreatedAt:       timestamppb.New(d.CreatedAt),
		UpdatedAt:       timestamppb.New(d.UpdatedAt),
		StartedAt:       optionalTimestamp(d.StartedAt),
		CompletedAt:     optionalTimestamp(d.CompletedAt),
		SpecVersion:     d.SpecVersion,
		Spec:            spec,
		ActorType:       parseDeploymentActorType(d.ActorType),
		ActorId:         d.ActorID.String(),
		ActorName:       row.ActorName,
		ActorAvatarUrl:  row.ActorAvatarUrl,
		Method:          parseDeploymentMethod(d.Method),
		Commit:          commit,
		CurrentRevision: row.CurrentRevision,
	}
	if err := setDeploymentRollout(deployment, row.RolloutSnapshot); err != nil {
		return nil, err
	}
	return deployment, nil
}

// DeploymentServer implements the DeploymentService gRPC server
type DeploymentServer struct {
	notifier       *pgnotify.Notifier
	db             *pgxpool.Pool
	queries        genDb.Querier
	resolver       ImageResolver
	registryHost   string
	defaults       servicedefaults.Defaults
	authz          *authz.Authorizer
	silentAfter    time.Duration
	maxSecretBytes int32
	counts         *configv1.CountLimits
	watchLimit     int32
	watchMu        sync.Mutex
	watchStreams   map[genDb.Entity]int32
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
	counts *configv1.CountLimits,
	watchLimit int32,
	notifier *pgnotify.Notifier,
) *DeploymentServer {
	return &DeploymentServer{
		notifier:       notifier,
		db:             db,
		queries:        queries,
		resolver:       resolver,
		registryHost:   registryHost,
		defaults:       defaults,
		authz:          authz.New(queries),
		silentAfter:    clusterSilentAfter,
		maxSecretBytes: maxSecretBytes,
		counts:         counts,
		watchLimit:     watchLimit,
		watchStreams:   make(map[genDb.Entity]int32),
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
		return nil, ErrResourceNotFound
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

	organizationID, counts, err := workspaceLimits(ctx, s.queries, s.counts, resource.WorkspaceID)
	if err != nil {
		return nil, err
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
		building := genDb.DeploymentStatusBuilding
		if rolloutErr := ensureNoRollout(ctx, qtx, resourceID, environmentID, region, building); rolloutErr != nil {
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
		deploymentID, txErr = createDeploymentWithCleanup(ctx, qtx, counts, genDb.CreateDeploymentParams{
			ResourceID:    resourceID,
			ClusterID:     cluster.ID,
			Region:        region,
			Replicas:      replicas,
			Status:        genDb.DeploymentStatusDeploying,
			IsActive:      true,
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
			Type:          events.DeploymentCreated,
			EnvironmentID: new(environmentID),
			WorkspaceID:   new(resource.WorkspaceID),
			ResourceID:    new(resource.ID),
			SubjectType:   events.SubjectDeployment,
			SubjectID:     new(deploymentID),
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

	row, err := s.queries.GetDeploymentView(ctx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeploymentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}

	required := actions.New(actions.GetDeployment, row.Deployment.ResourceID)
	if authErr := authorizeVisible(ctx, s.authz, required, ErrDeploymentNotFound); authErr != nil {
		return nil, authErr
	}

	deployment, err := deploymentToProto(row)
	if err != nil {
		return nil, err
	}
	return &deploymentv1.GetDeploymentResponse{Deployment: deployment}, nil
}

// ListDeployments lists deployments the caller can read for a resource or workspace environment.
func (s *DeploymentServer) ListDeployments(
	ctx context.Context,
	req *deploymentv1.ListDeploymentsRequest,
) (*deploymentv1.ListDeploymentsResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	var resourceID, workspaceID, environmentID *uuid.UUID
	for _, field := range []struct {
		name   string
		value  string
		target **uuid.UUID
	}{
		{"resource_id", req.GetResourceId(), &resourceID},
		{"workspace_id", req.GetWorkspaceId(), &workspaceID},
		{"environment_id", req.GetEnvironmentId(), &environmentID},
	} {
		if field.value == "" {
			continue
		}
		id, parseErr := parseID(field.name, field.value)
		if parseErr != nil {
			return nil, parseErr
		}
		*field.target = &id
	}
	var since, until *time.Time
	if req.GetSince() != nil {
		if err = req.GetSince().CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "invalid since").WithCause(err)
		}
		value := req.GetSince().AsTime()
		since = &value
	}
	if req.GetUntil() != nil {
		if err = req.GetUntil().CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "invalid until").WithCause(err)
		}
		value := req.GetUntil().AsTime()
		until = &value
	}
	readable := s.authz.ReadableEntities(p.Scopes)
	pageSize := normalizePageSize(req.GetPageSize())
	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}
	var rows []genDb.ListEnvironmentDeploymentsRow
	if resourceID != nil {
		resourceRows, listErr := s.queries.ListResourceDeployments(ctx, genDb.ListResourceDeploymentsParams{
			ResourceID: *resourceID, EnvironmentID: environmentID,
			SystemRead: readable.System, OrganizationIds: readable.Organizations,
			WorkspaceIds: readable.Workspaces, ResourceIds: readable.Resources,
			Since: since, Until: until, PageSize: pageSize, PageToken: cursor.ID, PageCreatedAt: cursor.CreatedAt,
		})
		err = listErr
		for _, row := range resourceRows {
			rows = append(rows, genDb.ListEnvironmentDeploymentsRow(row))
		}
	} else {
		rows, err = s.queries.ListEnvironmentDeployments(ctx, genDb.ListEnvironmentDeploymentsParams{
			WorkspaceID: *workspaceID, EnvironmentID: *environmentID,
			SystemRead: readable.System, OrganizationIds: readable.Organizations,
			WorkspaceIds: readable.Workspaces, ResourceIds: readable.Resources,
			Since: since, Until: until, PageSize: pageSize, PageToken: cursor.ID, PageCreatedAt: cursor.CreatedAt,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	var deployments []*deploymentv1.Deployment
	for _, row := range rows {
		deployment, err := deploymentToProto(genDb.GetDeploymentViewRow(row))
		if err != nil {
			return nil, err
		}
		deployment.Replicas = nil
		deployments = append(deployments, deployment)
	}
	token := nextPageToken(rows, pageSize, func(row genDb.ListEnvironmentDeploymentsRow) (uuid.UUID, time.Time) {
		return row.Deployment.ID, row.Deployment.CreatedAt
	})
	return &deploymentv1.ListDeploymentsResponse{Deployments: deployments, NextPageToken: token}, nil
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

func (s *DeploymentServer) acquireWatch(entity genDb.Entity) bool {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watchStreams[entity] >= s.watchLimit {
		return false
	}
	s.watchStreams[entity]++
	return true
}

func (s *DeploymentServer) releaseWatch(entity genDb.Entity) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watchStreams[entity]--
	if s.watchStreams[entity] == 0 {
		delete(s.watchStreams, entity)
	}
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

// workspaceLimits returns the organization of a workspace and its limits.
func workspaceLimits(
	ctx context.Context,
	q genDb.Querier,
	defaults *configv1.CountLimits,
	workspaceID uuid.UUID,
) (uuid.UUID, limits.Effective, error) {
	organizationID, err := q.GetOrganizationIDByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return uuid.UUID{}, limits.Effective{}, fmt.Errorf("get workspace organization: %w", err)
	}
	counts, err := limits.ForOrg(ctx, q, defaults, organizationID)
	if err != nil {
		return uuid.UUID{}, limits.Effective{}, err
	}
	return organizationID, counts, nil
}
