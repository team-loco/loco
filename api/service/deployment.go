package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

var (
	ErrDeploymentNotFound = rpcerr.NewCoded(connect.CodeNotFound, "deployment not found")
)

var deploymentStatuses = map[genDb.DeploymentStatus]deploymentv1.DeploymentStatus{
	genDb.DeploymentStatusBuilding:   deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_BUILDING,
	genDb.DeploymentStatusDeploying:  deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING,
	genDb.DeploymentStatusRunning:    deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING,
	genDb.DeploymentStatusDegraded:   deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEGRADED,
	genDb.DeploymentStatusCrashed:    deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED,
	genDb.DeploymentStatusSuperseded: deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED,
	genDb.DeploymentStatusFailed:     deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED,
	genDb.DeploymentStatusCanceled:   deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED,
}

// deploymentStatusNames returns the database names of statuses.
func deploymentStatusNames(statuses []deploymentv1.DeploymentStatus) []string {
	var names []string
	for name, status := range deploymentStatuses {
		if slices.Contains(statuses, status) {
			names = append(names, string(name))
		}
	}
	return names
}

var placementStatuses = map[genDb.PlacementStatus]deploymentv1.PlacementStatus{
	genDb.PlacementStatusPending:    deploymentv1.PlacementStatus_PLACEMENT_STATUS_PENDING,
	genDb.PlacementStatusDeploying:  deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEPLOYING,
	genDb.PlacementStatusRunning:    deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	genDb.PlacementStatusDegraded:   deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEGRADED,
	genDb.PlacementStatusCrashed:    deploymentv1.PlacementStatus_PLACEMENT_STATUS_CRASHED,
	genDb.PlacementStatusFailed:     deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
	genDb.PlacementStatusCanceled:   deploymentv1.PlacementStatus_PLACEMENT_STATUS_CANCELED,
	genDb.PlacementStatusSuperseded: deploymentv1.PlacementStatus_PLACEMENT_STATUS_SUPERSEDED,
}

var deploymentTriggers = map[genDb.DeploymentTrigger]deploymentv1.DeploymentTrigger{
	genDb.DeploymentTriggerDeploy:   deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_DEPLOY,
	genDb.DeploymentTriggerRedeploy: deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY,
	genDb.DeploymentTriggerRollback: deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_ROLLBACK,
	genDb.DeploymentTriggerScale:    deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_SCALE,
}

var deploymentTriggersFromProto = map[deploymentv1.DeploymentTrigger]genDb.DeploymentTrigger{
	deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_UNSPECIFIED: genDb.DeploymentTriggerDeploy,
	deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_DEPLOY:      genDb.DeploymentTriggerDeploy,
	deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_REDEPLOY:    genDb.DeploymentTriggerRedeploy,
	deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_ROLLBACK:    genDb.DeploymentTriggerRollback,
}

// placementView is one element of the placements JSON the deployment queries return.
type placementView struct {
	ClusterID    uuid.UUID             `json:"clusterId"`
	Region       string                `json:"region"`
	Replicas     int32                 `json:"replicas"`
	Status       genDb.PlacementStatus `json:"status"`
	Image        string                `json:"image"`
	StartedAt    *time.Time            `json:"startedAt"`
	CompletedAt  *time.Time            `json:"completedAt"`
	SupersededBy *uuid.UUID            `json:"supersededBy"`
	Previous     *uuid.UUID            `json:"previous"`
	Live         bool                  `json:"live"`
	Revision     int64                 `json:"revision"`
	Rollout      json.RawMessage       `json:"rollout"`
	Observed     json.RawMessage       `json:"observed"`
}

func deploymentToProto(row genDb.GetDeploymentViewRow) (*deploymentv1.Deployment, error) {
	d := row.Deployment
	spec, err := converter.DeserializeDeploymentSpec(d.Spec, row.ResourceType)
	if err != nil {
		return nil, fmt.Errorf("decode spec of deployment %s: %w", d.ID, err)
	}
	var commit *deploymentv1.SourceCommit
	if d.CommitSha != nil {
		commit = &deploymentv1.SourceCommit{Sha: *d.CommitSha, Dirty: d.CommitDirty, Message: row.CommitMessage}
	}
	var views []placementView
	if err := json.Unmarshal(row.Placements, &views); err != nil {
		return nil, fmt.Errorf("decode placements of deployment %s: %w", d.ID, err)
	}
	placements := make([]*deploymentv1.DeploymentPlacement, 0, len(views))
	for _, view := range views {
		placement := &deploymentv1.DeploymentPlacement{
			ClusterId:       view.ClusterID.String(),
			Region:          view.Region,
			DesiredReplicas: view.Replicas,
			Status:          placementStatuses[view.Status],
			Image:           view.Image,
			StartedAt:       optionalTimestamp(view.StartedAt),
			CompletedAt:     optionalTimestamp(view.CompletedAt),
			CurrentRevision: view.Revision,
			Live:            view.Live,
		}
		if view.Previous != nil {
			placement.PreviousDeploymentId = view.Previous.String()
		}
		if view.SupersededBy != nil {
			placement.SupersededBy = view.SupersededBy.String()
		}
		if err := setPlacementRollout(
			placement,
			spec.GetService().GetBuild().GetBuildId(),
			view.Rollout,
			view.Observed,
		); err != nil {
			return nil, err
		}
		placements = append(placements, placement)
	}
	source := ""
	if d.SourceDeploymentID != nil {
		source = d.SourceDeploymentID.String()
	}
	return &deploymentv1.Deployment{
		Id:                 d.ID.String(),
		ResourceId:         d.ResourceID.String(),
		EnvironmentId:      d.EnvironmentID.String(),
		Status:             deploymentStatuses[d.Status],
		CreatedAt:          timestamppb.New(d.CreatedAt),
		UpdatedAt:          timestamppb.New(d.UpdatedAt),
		StartedAt:          optionalTimestamp(d.StartedAt),
		CompletedAt:        optionalTimestamp(d.CompletedAt),
		SpecVersion:        d.SpecVersion,
		Spec:               spec,
		ActorType:          parseDeploymentActorType(d.ActorType),
		ActorId:            d.ActorID.String(),
		ActorName:          row.ActorName,
		ActorAvatarUrl:     row.ActorAvatarUrl,
		Method:             parseDeploymentMethod(d.Method),
		Commit:             commit,
		Placements:         placements,
		Trigger:            deploymentTriggers[d.Trigger],
		SourceDeploymentId: source,
		CanceledByName:     row.CanceledByName,
		ResourceName:       row.ResourceName,
	}, nil
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

var errSourceDeployment = rpcerr.NewCoded(
	connect.CodeInvalidArgument,
	"a redeploy or rollback names the deployment it copies, of the same resource and environment",
)

// CreateDeployment creates one deployment of the resource in the environment, placed in every
// requested region, or in every enabled region when the request names none.
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

	serviceSpec := req.GetSpec().GetService()
	if serviceSpec == nil {
		return nil, errOnlyServiceResources
	}

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
	trigger := deploymentTriggersFromProto[req.GetTrigger()]
	source, err := s.sourceDeployment(ctx, req, resourceID, environmentID, trigger)
	if err != nil {
		return nil, err
	}

	clusters, err := eligibleClusters(ctx, s.queries, s.silentAfter, s.defaults.TargetPlatform)
	if err != nil {
		return nil, fmt.Errorf("get active clusters: %w", err)
	}

	requestedBuild := serviceSpec.GetBuild()
	pinnedBuild, err := pinBuildSource(
		ctx,
		s.queries,
		s.resolver,
		s.registryHost,
		resourceID,
		requestedBuild,
		s.defaults.TargetPlatform,
	)
	if err != nil {
		return nil, err
	}
	originalSpec := req.GetSpec()
	requestSpec := proto.CloneOf(originalSpec)
	requestSpec.GetService().Build = pinnedBuild

	_, counts, err := workspaceLimits(ctx, s.queries, s.counts, resource.WorkspaceID)
	if err != nil {
		return nil, err
	}

	var deploymentID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockOrganization(ctx, qtx, resource.WorkspaceID); lockErr != nil {
			return lockErr
		}
		lockErr := lockDeploymentEnvironments(ctx, qtx, resourceID, environmentID, req.GetRemoveHealthCheck())
		if lockErr != nil {
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
		render, renderErr := newPlacementRenderer(ctx, qtx, resource, environmentID, s.defaults)
		if renderErr != nil {
			return renderErr
		}
		regions := req.GetRegions()
		if len(regions) == 0 {
			regions = enabledRegions(render.resourceSpec.GetService().GetRegions())
		}
		targets, targetErr := placementTargets(clusters, regions, string(s.defaults.TargetPlatform))
		if targetErr != nil {
			return targetErr
		}

		if req.GetRemoveHealthCheck() {
			removeErr := removeHealthCheck(ctx, qtx, resourceID, render.resourceSpec.GetService())
			if removeErr != nil {
				return removeErr
			}
		}
		requestSpec.GetService().Platform = string(s.defaults.TargetPlatform)
		mergedServiceSpec, mergeErr := mergeRequestSpec(render.resourceSpec, requestSpec, regions, s.defaults)
		if mergeErr != nil {
			return mergeErr
		}

		resolved, resolveErr := s.resolveVariables(
			ctx,
			qtx,
			resource,
			environmentID,
			render.primaryHostname(),
			mergedServiceSpec,
		)
		if resolveErr != nil {
			return resolveErr
		}

		var txErr error
		deploymentID, txErr = createDeployment(ctx, qtx, &counts, newDeployment{
			params: genDb.CreateDeploymentParams{
				ResourceID:         resourceID,
				EnvironmentID:      environmentID,
				ActorType:          principal.ActorType,
				ActorID:            principal.ActorID,
				Method:             principal.Method,
				CommitSha:          sourceCommitSHA(req.GetCommit()),
				CommitDirty:        req.GetCommit().GetDirty(),
				Trigger:            trigger,
				SourceDeploymentID: sourceID(source),
			},
			service:  mergedServiceSpec,
			resolved: resolved,
			targets:  targets,
			render:   render.payload,
		})
		if txErr != nil {
			return txErr
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
			return bumpErr
		}
		return recordDeploymentCreated(ctx, qtx, resource, environmentID, deploymentID, regions)
	})
	if err != nil {
		return nil, err
	}

	return &deploymentv1.CreateDeploymentResponse{
		DeploymentId: deploymentID.String(),
		Build:        pinnedBuild,
	}, nil
}

// lockDeploymentEnvironments locks the deployment's environment, or every environment of the
// workspace when the deployment removes the resource's health check, which they all inherit.
func lockDeploymentEnvironments(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID, environmentID uuid.UUID,
	removesHealthCheck bool,
) error {
	if removesHealthCheck {
		if err := lockResourceEnvironments(ctx, qtx, resourceID); err != nil {
			return err
		}
	}
	_, err := lockEnvironment(ctx, qtx, environmentID)
	return err
}

// removeHealthCheck removes the resource's health check, which deployments without one inherit, and
// advances every environment's revision. The caller holds the workspace's environments.
func removeHealthCheck(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	spec *resourcev1.ServiceSpec,
) error {
	if spec.GetHealthCheck() == nil {
		return nil
	}
	spec.HealthCheck = nil
	specJSON, err := protojson.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal resource spec: %w", err)
	}
	if err := qtx.UpdateResourceSpec(ctx, genDb.UpdateResourceSpecParams{ID: resourceID, Spec: specJSON}); err != nil {
		return fmt.Errorf("remove resource health check: %w", err)
	}
	if err := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); err != nil {
		return err
	}
	return events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceUpdated,
		ResourceID:  new(resourceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
	})
}

// mergeRequestSpec fills a request's service spec for the target regions and checks its sizes.
func mergeRequestSpec(
	resourceSpec *resourcev1.ResourceSpec,
	requestSpec *deploymentv1.DeploymentSpec,
	regions []string,
	defaults servicedefaults.Defaults,
) (*deploymentv1.ServiceDeploymentSpec, error) {
	merged, err := converter.MergeDeploymentSpec(resourceSpec, requestSpec, regions, defaults)
	if errors.Is(err, converter.ErrRegionNotFound) || errors.Is(err, converter.ErrRegionDisabled) ||
		errors.Is(err, converter.ErrRegionNotTargeted) {
		return nil, &invalidInputError{public: publicInvalidRequest, err: err}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: merge deployment spec: %w", errDesiredSpec, err)
	}
	if quantityErr := validateQuantities(merged.GetService()); quantityErr != nil {
		return nil, &invalidInputError{public: publicInvalidRequest, err: quantityErr}
	}
	return merged.GetService(), nil
}

// sourceDeployment is the deployment a redeploy or rollback copies, which must belong to the
// same resource and environment; a deploy names none.
func (s *DeploymentServer) sourceDeployment(
	ctx context.Context,
	req *deploymentv1.CreateDeploymentRequest,
	resourceID, environmentID uuid.UUID,
	trigger genDb.DeploymentTrigger,
) (*genDb.Deployment, error) {
	if trigger == genDb.DeploymentTriggerDeploy {
		if req.GetSourceDeploymentId() != "" {
			return nil, errSourceDeployment
		}
		return nil, nil
	}
	if req.GetSourceDeploymentId() == "" {
		return nil, errSourceDeployment
	}
	source, err := s.queries.GetDeploymentByID(ctx, uuid.MustParse(req.GetSourceDeploymentId()))
	if errors.Is(err, pgx.ErrNoRows) || err == nil &&
		(source.ResourceID != resourceID || source.EnvironmentID != environmentID) {
		return nil, errSourceDeployment
	}
	if err != nil {
		return nil, fmt.Errorf("get source deployment: %w", err)
	}
	return &source, nil
}

func sourceID(source *genDb.Deployment) *uuid.UUID {
	if source == nil {
		return nil
	}
	return &source.ID
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
	if req.GetStartTime() != nil {
		if err = req.GetStartTime().CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "invalid start_time").WithCause(err)
		}
		value := req.GetStartTime().AsTime()
		since = &value
	}
	if req.GetEndTime() != nil {
		if err = req.GetEndTime().CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, "invalid end_time").WithCause(err)
		}
		value := req.GetEndTime().AsTime()
		until = &value
	}
	statuses := deploymentStatusNames(req.GetStatuses())
	var buildID *string
	if req.GetBuildId() != "" {
		buildID = new(req.GetBuildId())
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
			ResourceID:      *resourceID,
			EnvironmentID:   environmentID,
			SystemRead:      readable.System,
			OrganizationIds: readable.Organizations,
			WorkspaceIds:    readable.Workspaces,
			ResourceIds:     readable.Resources,
			Since:           since,
			Until:           until,
			Statuses:        statuses,
			BuildID:         buildID,
			PageSize:        pageSize + 1,
			PageToken:       cursor.ID,
			PageCreatedAt:   cursor.CreatedAt,
		})
		err = listErr
		for _, row := range resourceRows {
			rows = append(rows, genDb.ListEnvironmentDeploymentsRow(row))
		}
	} else {
		rows, err = s.queries.ListEnvironmentDeployments(ctx, genDb.ListEnvironmentDeploymentsParams{
			WorkspaceID:     *workspaceID,
			EnvironmentID:   *environmentID,
			SystemRead:      readable.System,
			OrganizationIds: readable.Organizations,
			WorkspaceIds:    readable.Workspaces,
			ResourceIds:     readable.Resources,
			Since:           since,
			Until:           until,
			Statuses:        statuses,
			BuildID:         buildID,
			PageSize:        pageSize + 1,
			PageToken:       cursor.ID,
			PageCreatedAt:   cursor.CreatedAt,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	rows, token := trimPage(rows, pageSize, func(row genDb.ListEnvironmentDeploymentsRow) (uuid.UUID, time.Time) {
		return row.Deployment.ID, row.Deployment.CreatedAt
	})
	var deployments []*deploymentv1.Deployment
	for _, row := range rows {
		deployment, convErr := deploymentToProto(genDb.GetDeploymentViewRow(row))
		if convErr != nil {
			return nil, convErr
		}
		for _, placement := range deployment.GetPlacements() {
			placement.Replicas = nil
		}
		deployments = append(deployments, deployment)
	}
	total, err := totalCount(ctx, req.GetIncludeTotalCount(), func(ctx context.Context, limit int32) (int64, error) {
		return s.queries.CountDeployments(ctx, genDb.CountDeploymentsParams{
			ResourceID: resourceID, WorkspaceID: workspaceID, EnvironmentID: environmentID,
			SystemRead: readable.System, OrganizationIds: readable.Organizations,
			WorkspaceIds: readable.Workspaces, ResourceIds: readable.Resources,
			Since: since, Until: until, Statuses: statuses, BuildID: buildID, RowLimit: limit,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("count deployments: %w", err)
	}
	return &deploymentv1.ListDeploymentsResponse{Deployments: deployments, NextPageToken: token, TotalCount: total}, nil
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
		if _, lockErr := lockEnvironment(ctx, qtx, deployment.EnvironmentID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resource.ID); lockErr != nil {
			return lockErr
		}
		live, listErr := listLiveDeploymentsForEnvironment(ctx, qtx, resource.ID, deployment.EnvironmentID)
		if listErr != nil {
			return listErr
		}
		for _, region := range live {
			if region.ID != deploymentID {
				continue
			}
			if retireErr := retireDeployment(ctx, qtx, resource, region); retireErr != nil {
				return retireErr
			}
		}
		return bumpEnvironmentRevision(ctx, qtx, deployment.EnvironmentID)
	})
	if err != nil {
		return nil, fmt.Errorf("delete deployment: %w", err)
	}

	return &deploymentv1.DeleteDeploymentResponse{}, nil
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
	for _, target := range service.GetRegions() {
		if err := validateSize(target.GetCpu(), target.GetMemory()); err != nil {
			return err
		}
	}
	return nil
}

// validateSize rejects a cpu or memory that is not a Kubernetes quantity.
func validateSize(cpu, memory string) error {
	if _, err := servicedefaults.ParseCPU(cpu); err != nil {
		return err
	}
	_, err := servicedefaults.ParseMemory(memory)
	return err
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
