package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/platformdomain"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/reqctx"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/rpcerr"
	"github.com/team-loco/loco/internal/workloadplatform"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var (
	ErrResourceNotFound      = rpcerr.NewCoded(connect.CodeNotFound, "resource not found")
	ErrDomainNotFound        = rpcerr.NewCoded(connect.CodeNotFound, "domain not found")
	ErrResourceNameNotUnique = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"resource name already exists in this workspace",
	)
	errInvalidResourceName = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"resource name must be 1 to 63 lowercase letters, digits and hyphens, "+
			"starting and ending with a letter or digit, and not localhost or resource-<UUID>",
	)
	ErrClusterNotFound     = errors.New("cluster not found")
	ErrInvalidResourceType = errors.New("invalid resource type")

	errDomainInUse          = rpcerr.NewCoded(connect.CodeAlreadyExists, "domain already in use")
	errOnlyServiceResources = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"only service resources are currently supported",
	)
	errScaleNothingRequested = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"at least one of replicas, cpu, or memory must be provided",
	)
	errScaleUnchanged = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"scaling values must be different from current deployment",
	)
	errNoActiveDeployment = rpcerr.NewCoded(
		connect.CodeNotFound,
		"resource has no active deployment in the environment",
	)
)

type ResourceServer struct {
	resourcev1connect.UnimplementedResourceServiceHandler
	db                 *pgxpool.Pool
	queries            genDb.Querier
	authz              *authz.Authorizer
	defaults           servicedefaults.Defaults
	clusterSilentAfter time.Duration
	counts             *configv1.CountLimits
}

// NewResourceServer creates a new ResourceServer instance
func NewResourceServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
	counts *configv1.CountLimits,
) *ResourceServer {
	return &ResourceServer{
		db:                 db,
		queries:            queries,
		authz:              authz.New(queries),
		defaults:           defaults,
		clusterSilentAfter: clusterSilentAfter,
		counts:             counts,
	}
}

// CreateResource creates a new resource
func (s *ResourceServer) CreateResource(
	ctx context.Context,
	req *resourcev1.CreateResourceRequest,
) (*resourcev1.CreateResourceResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.CreateResource, workspaceID)); err != nil {
		return nil, err
	}

	if !validResourceName(req.GetName()) {
		return nil, errInvalidResourceName
	}

	if req.GetSpec().GetService() == nil {
		return nil, errOnlyServiceResources
	}
	if req.GetType() != resourcev1.ResourceType_RESOURCE_TYPE_SERVICE {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).
			WithCause(ErrInvalidResourceType)
	}

	specJSON, err := protojson.Marshal(req.GetSpec().GetService())
	if err != nil {
		return nil, fmt.Errorf("marshal resource spec: %w", err)
	}

	params := genDb.CreateResourceParams{
		WorkspaceID: workspaceID,
		CreatedBy:   p.ActorID,
		Name:        req.GetName(),
		Type:        genDb.ResourceTypeService,
		Spec:        specJSON,
		SpecVersion: specVersion,
		Description: req.GetDescription(),
	}
	var resourceID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if limitErr := checkWorkspaceLimit(
			ctx, qtx, s.counts, limits.ResourcesPerWorkspace, workspaceID, qtx.CountWorkspaceResources,
		); limitErr != nil {
			return limitErr
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, workspaceID); lockErr != nil {
			return lockErr
		}
		var createErr error
		resourceID, createErr = qtx.CreateResource(ctx, params)
		if isUniqueViolation(createErr, pgResourceNameUnique) {
			return ErrResourceNameNotUnique.Detail("%s", req.GetName())
		}
		if createErr != nil {
			return fmt.Errorf("insert resource: %w", createErr)
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}
		if req.GetDomain() != nil {
			if domainErr := s.createPlatformDomain(ctx, qtx, resourceID, workspaceID, req); domainErr != nil {
				return domainErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ResourceCreated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectResource,
			SubjectID:   new(resourceID),
			Data:        map[string]any{events.FieldName: req.GetName()},
		})
	})
	if err != nil {
		return nil, err
	}

	return &resourcev1.CreateResourceResponse{ResourceId: resourceID.String()}, nil
}

func (s *ResourceServer) createPlatformDomain(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	workspaceID uuid.UUID,
	req *resourcev1.CreateResourceRequest,
) error {
	env, err := resourceDomainEnvironment(ctx, qtx, workspaceID, req.EnvironmentId)
	if err != nil {
		return err
	}
	regions := enabledRegions(req.GetSpec().GetService().GetRegions())
	check, err := resourceDomainCheck(
		ctx,
		qtx,
		regions,
		nil,
		s.clusterSilentAfter,
		s.defaults.TargetPlatform,
	)
	if err != nil {
		return fmt.Errorf("list domain clusters: %w", err)
	}
	hostname := req.GetDomain().GetSubdomain() + "." + req.GetDomain().GetPlatformDomain()
	platform, label, err := platformdomain.Check(hostname, check.clusters, check.targets)
	if err != nil {
		return fmt.Errorf("check the resource domain: %w", err)
	}

	_, err = qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
		ResourceID:     resourceID,
		EnvironmentID:  env.ID,
		Domain:         hostname,
		DomainSource:   genDb.DomainSourcePlatformProvided,
		SubdomainLabel: &label,
		PlatformDomain: &platform,
		IsPrimary:      true,
	})
	if isDomainTaken(err) {
		return errDomainInUse
	}
	if err != nil {
		return fmt.Errorf("insert resource domain: %w", err)
	}
	return nil
}

func resourceDomainEnvironment(
	ctx context.Context,
	qtx *genDb.Queries,
	workspaceID uuid.UUID,
	requestedID *string,
) (genDb.Environment, error) {
	if requestedID == nil {
		return workspaceDefaultEnvironment(ctx, qtx, workspaceID)
	}
	id, err := parseID("environment_id", *requestedID)
	if err != nil {
		return genDb.Environment{}, err
	}
	env, err := qtx.GetEnvironmentByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Environment{}, ErrEnvironmentNotFound
	}
	if err != nil {
		return genDb.Environment{}, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != workspaceID {
		return genDb.Environment{}, ErrEnvironmentNotFound
	}
	return env, nil
}

// GetResource retrieves a resource by ID
func (s *ResourceServer) GetResource(
	ctx context.Context,
	req *resourcev1.GetResourceRequest,
) (*resourcev1.GetResourceResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}

	var resourceID uuid.UUID
	switch key := req.GetKey().(type) {
	case *resourcev1.GetResourceRequest_ResourceId:
		resourceID, err = parseID("resource_id", key.ResourceId)
	case *resourcev1.GetResourceRequest_NameKey:
		resourceID, err = s.resourceIDByName(ctx, p, key.NameKey)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, "resource_id or name_key is required")
	}
	if err != nil {
		return nil, err
	}

	if err = check(ctx, s.authz, p, actions.New(actions.GetResource, resourceID)); err != nil {
		return nil, err
	}

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, res.ID)
	if err != nil {
		return nil, fmt.Errorf("list resource domains: %w", err)
	}

	statuses, err := s.environmentStatuses(ctx, []uuid.UUID{res.ID})
	if err != nil {
		return nil, fmt.Errorf("derive resource status: %w", err)
	}

	resource, err := dbResourceToProto(res, statuses[res.ID], resourceDomains)
	if err != nil {
		return nil, err
	}
	return &resourcev1.GetResourceResponse{Resource: resource}, nil
}

func (s *ResourceServer) resourceIDByName(
	ctx context.Context,
	p reqctx.Principal,
	nameKey *resourcev1.GetResourceNameKey,
) (uuid.UUID, error) {
	workspaceID, err := parseID("workspace_id", nameKey.GetWorkspaceId())
	if err != nil {
		return uuid.UUID{}, err
	}
	name := nameKey.GetName()
	res, err := s.queries.GetResourceByNameAndWorkspace(ctx, genDb.GetResourceByNameAndWorkspaceParams{
		WorkspaceID: workspaceID,
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		workspaceRead := actions.New(actions.ListResources, workspaceID)
		if verifyErr := check(ctx, s.authz, p, workspaceRead); verifyErr != nil {
			return uuid.UUID{}, verifyErr
		}
		return uuid.UUID{}, ErrResourceNotFound
	}
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("get resource by name: %w", err)
	}
	return res.ID, nil
}

// ListWorkspaceResources lists all resources in a workspace
func (s *ResourceServer) ListWorkspaceResources(
	ctx context.Context,
	req *resourcev1.ListWorkspaceResourcesRequest,
) (*resourcev1.ListWorkspaceResourcesResponse, error) {
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListResources, wsID); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	dbResources, err := s.queries.ListResourcesForWorkspace(ctx, genDb.ListResourcesForWorkspaceParams{
		WorkspaceID:   wsID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	resourceIDs := make([]uuid.UUID, len(dbResources))
	for i, dbResource := range dbResources {
		resourceIDs[i] = dbResource.ID
	}

	allDomains, err := s.queries.ListResourceDomainsForResources(ctx, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("list resource domains: %w", err)
	}

	statuses, err := s.environmentStatuses(ctx, resourceIDs)
	if err != nil {
		return nil, fmt.Errorf("derive resource statuses: %w", err)
	}
	for _, environments := range statuses {
		for _, environment := range environments {
			for _, region := range environment.GetRegions() {
				region.Replicas = nil
			}
		}
	}

	domainsByResource := make(map[uuid.UUID][]genDb.ResourceDomain, len(dbResources))
	for _, domain := range allDomains {
		domainsByResource[domain.ResourceID] = append(domainsByResource[domain.ResourceID], domain)
	}

	resources := make([]*resourcev1.Resource, 0, len(dbResources))
	for _, dbResource := range dbResources {
		resourceDomains := domainsByResource[dbResource.ID]
		resourceStatuses := statuses[dbResource.ID]
		resource, err := dbResourceToProto(dbResource, resourceStatuses, resourceDomains)
		if err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}

	token := nextPageToken(
		dbResources,
		pageSize,
		func(row genDb.Resource) (uuid.UUID, time.Time) { return row.ID, row.CreatedAt },
	)

	return &resourcev1.ListWorkspaceResourcesResponse{
		Resources:     resources,
		NextPageToken: token,
	}, nil
}

// UpdateResource updates a resource
func (s *ResourceServer) UpdateResource(
	ctx context.Context,
	req *resourcev1.UpdateResourceRequest,
) (*resourcev1.UpdateResourceResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.UpdateResource, resourceID); err != nil {
		return nil, err
	}

	updateParams := genDb.UpdateResourceParams{
		ID:          resourceID,
		DisplayName: req.DisplayName,
		Description: req.Description,
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if req.GetRegions() != nil {
			if lockErr := ensureOrganizationActive(ctx, qtx, resourceID); lockErr != nil {
				return lockErr
			}
		}
		if lockErr := lockResourceEnvironments(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		resource, getErr := qtx.GetResourceByID(ctx, resourceID)
		if getErr != nil {
			return fmt.Errorf("get resource: %w", getErr)
		}
		if req.GetRegions() != nil {
			if updateErr := s.updateRegions(ctx, qtx, resource, req); updateErr != nil {
				return updateErr
			}
		}
		if req.RollbackOnFailure != nil {
			if resource.Type != genDb.ResourceTypeService {
				return errOnlyServiceResources
			}
			if req.GetRegions() != nil {
				resource, getErr = qtx.GetResourceByID(ctx, resourceID)
				if getErr != nil {
					return fmt.Errorf("get updated resource: %w", getErr)
				}
			}
			spec, specErr := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
			if specErr != nil {
				return specErr
			}
			service := spec.GetService()
			service.RollbackOnFailure = req.GetRollbackOnFailure()
			encoded, marshalErr := protojson.Marshal(service)
			if marshalErr != nil {
				return fmt.Errorf("marshal service setting: %w", marshalErr)
			}
			if updateErr := qtx.UpdateResourceSpec(
				ctx,
				genDb.UpdateResourceSpecParams{ID: resourceID, Spec: encoded},
			); updateErr != nil {
				return fmt.Errorf("update service setting: %w", updateErr)
			}
		}
		if _, updateErr := qtx.UpdateResource(ctx, updateParams); updateErr != nil {
			return fmt.Errorf("update resource: %w", updateErr)
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ResourceUpdated,
			ResourceID:  new(resourceID),
			SubjectType: events.SubjectResource,
			SubjectID:   new(resourceID),
		})
	})
	if err != nil {
		return nil, err
	}

	return &resourcev1.UpdateResourceResponse{ResourceId: req.GetResourceId()}, nil
}

// DeleteResource deletes a resource
func (s *ResourceServer) DeleteResource(
	ctx context.Context,
	req *resourcev1.DeleteResourceRequest,
) (*resourcev1.DeleteResourceResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.DeleteResource, resourceID); err != nil {
		return nil, err
	}

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, res.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if removeErr := removeResourcePlacements(ctx, qtx, res.ID); removeErr != nil {
			return removeErr
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}

		if deleteErr := qtx.DeleteResource(ctx, resourceID); deleteErr != nil {
			return fmt.Errorf("delete resource: %w", deleteErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ResourceDeleted,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectResource,
			SubjectID:   new(resourceID),
			Data:        map[string]any{events.FieldName: res.Name},
		})
	})
	if err != nil {
		return nil, fmt.Errorf("delete resource: %w", err)
	}

	return &resourcev1.DeleteResourceResponse{}, nil
}

// ListRegions lists cluster regions and their availability for an environment.
func (s *ResourceServer) ListRegions(
	ctx context.Context,
	req *resourcev1.ListRegionsRequest,
) (*resourcev1.ListRegionsResponse, error) {
	environmentID := uuid.MustParse(req.GetEnvironmentId())
	_, err := visibleEnvironment(ctx, s.queries, s.authz, environmentID, actions.ListRegions)
	if err != nil {
		return nil, err
	}
	clusters, err := s.queries.ListClusterRegions(ctx, genDb.ListClusterRegionsParams{
		Platform:   string(s.defaults.TargetPlatform),
		HeardSince: time.Now().Add(-s.clusterSilentAfter),
	})
	if err != nil {
		return nil, fmt.Errorf("list cluster regions: %w", err)
	}
	regions := make([]*resourcev1.RegionInfo, 0, len(clusters))
	for _, cluster := range clusters {
		availability := resourcev1.RegionAvailability_REGION_AVAILABILITY_OFFLINE
		if cluster.Available {
			availability = resourcev1.RegionAvailability_REGION_AVAILABILITY_AVAILABLE
		}
		regions = append(regions, &resourcev1.RegionInfo{
			Region:       cluster.Region,
			IsDefault:    cluster.IsDefault,
			HealthStatus: derefString(cluster.HealthStatus),
			Availability: availability,
		})
	}
	return &resourcev1.ListRegionsResponse{Regions: regions}, nil
}

// ScaleResource scales a resource by creating a new deployment with updated resources
func (s *ResourceServer) ScaleResource(
	ctx context.Context,
	req *resourcev1.ScaleResourceRequest,
) (*resourcev1.ScaleResourceResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ScaleResource, resourceID); err != nil {
		return nil, err
	}

	if err = validateScaleRequest(req); err != nil {
		return nil, err
	}

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}

	env, err := s.queries.GetEnvironmentByID(ctx, uuid.MustParse(req.GetEnvironmentId()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != res.WorkspaceID {
		return nil, ErrEnvironmentNotFound
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockOrganization(ctx, qtx, res.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, res.WorkspaceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		currentResource, getErr := qtx.GetResourceByID(ctx, resourceID)
		if getErr != nil {
			return fmt.Errorf("read resource before scaling: %w", getErr)
		}
		return s.scaleCurrentDeployments(ctx, qtx, currentResource, env.ID, req)
	})
	if err != nil {
		return nil, err
	}
	return &resourcev1.ScaleResourceResponse{}, nil
}

// scaleCurrentDeployments creates one deployment per deployment live in the scaled regions,
// copying it with the new sizing; regions that run the same deployment scale together.
func (s *ResourceServer) scaleCurrentDeployments(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	environmentID uuid.UUID,
	req *resourcev1.ScaleResourceRequest,
) error {
	resourceID := res.ID
	groups, err := liveDeploymentsByRegion(ctx, qtx, resourceID, environmentID, req.GetRegion())
	if err != nil {
		return err
	}

	plans := make([]regionRedeploy, 0, len(groups))
	for _, group := range groups {
		current := group[0]
		serviceDeploymentSpec, specErr := currentServiceSpec(current.Deployment, res.Type)
		if specErr != nil {
			return specErr
		}
		targets := make(map[string]*deploymentv1.RegionDeploymentTarget, len(group))
		changed := false
		for _, region := range group {
			target := serviceDeploymentSpec.GetRegions()[region.Region]
			if target == nil {
				return errPreviousDeploymentNoSpec
			}
			changed = changed || scaleChangesDeployment(req, target, region.Replicas)
			applyScale(req, target)
			targets[region.Region] = target
		}
		if !changed {
			continue
		}
		serviceDeploymentSpec.Regions = targets

		plainEnv, envErr := desiredEnv(ctx, qtx, resourceID, current.EnvironmentID, current.ClusterID)
		if envErr != nil {
			return fmt.Errorf("read scaling environment: %w", envErr)
		}
		serviceDeploymentSpec.Env = plainEnv
		plans = append(plans, regionRedeploy{source: group, service: serviceDeploymentSpec})
	}

	if len(plans) == 0 {
		return errScaleUnchanged
	}

	scaled := map[string]any{events.FieldEnvironmentID: environmentID.String()}
	if req.Replicas != nil {
		scaled[events.FieldReplicas] = req.GetReplicas()
	}
	if req.Cpu != nil {
		scaled[events.FieldCPU] = req.GetCpu()
	}
	if req.Memory != nil {
		scaled[events.FieldMemory] = req.GetMemory()
	}
	if req.Region != nil {
		scaled[events.FieldRegion] = req.GetRegion()
	}
	scaledEvent := events.Event{
		Type:          events.ResourceScaled,
		EnvironmentID: new(environmentID),
		WorkspaceID:   new(res.WorkspaceID),
		SubjectType:   events.SubjectResource,
		SubjectID:     new(resourceID),
		Data:          scaled,
	}
	if err := s.redeployRegions(ctx, qtx, res, environmentID, plans, scaledEvent); err != nil {
		return err
	}
	if err := qtx.BumpResourceRevision(ctx, resourceID); err != nil {
		return fmt.Errorf("bump resource revision: %w", err)
	}
	return nil
}

// regionRedeploy is a copy of the deployment live in some regions with a new spec.
type regionRedeploy struct {
	source  []liveDeployment
	service *deploymentv1.ServiceDeploymentSpec
}

var (
	errNoDeploymentInRegion = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"resource has no active deployment in region",
	)
	errPreviousDeploymentNoSpec = rpcerr.NewCoded(connect.CodeInvalidArgument, "previous deployment has no spec")
)

// liveDeploymentsByRegion returns the regions of the environment, or the requested one, grouped
// by the deployment live there, newest deployment first.
func liveDeploymentsByRegion(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	environmentID uuid.UUID,
	requestedRegion string,
) ([][]liveDeployment, error) {
	live, err := listLiveDeploymentsForEnvironment(ctx, qtx, resourceID, environmentID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var groups [][]liveDeployment
	for _, region := range live {
		if seen[region.Region] || requestedRegion != "" && region.Region != requestedRegion {
			continue
		}
		seen[region.Region] = true
		index := slices.IndexFunc(groups, func(group []liveDeployment) bool { return group[0].ID == region.ID })
		if index < 0 {
			groups = append(groups, []liveDeployment{region})
			continue
		}
		groups[index] = append(groups[index], region)
	}
	if len(groups) > 0 {
		return groups, nil
	}
	if requestedRegion != "" {
		return nil, errNoDeploymentInRegion.Detail("%s", requestedRegion)
	}
	return nil, errNoActiveDeployment
}

func currentServiceSpec(
	current genDb.Deployment,
	resourceType genDb.ResourceType,
) (*deploymentv1.ServiceDeploymentSpec, error) {
	if len(current.Spec) == 0 {
		return nil, errPreviousDeploymentNoSpec
	}

	deploymentSpec, err := converter.DeserializeDeploymentSpec(current.Spec, resourceType)
	if err != nil {
		return nil, fmt.Errorf("decode deployment %s spec: %w", current.ID, err)
	}

	serviceDeploymentSpec := deploymentSpec.GetService()
	if serviceDeploymentSpec == nil {
		return nil, errOnlyServiceResources
	}

	return serviceDeploymentSpec, nil
}

func (s *ResourceServer) redeployRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	environmentID uuid.UUID,
	plans []regionRedeploy,
	ev events.Event,
) error {
	principal, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	_, counts, err := workspaceLimits(ctx, qtx, s.counts, res.WorkspaceID)
	if err != nil {
		return err
	}
	render, err := newPlacementRenderer(ctx, qtx, res, environmentID, s.defaults)
	if err != nil {
		return err
	}
	if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
		return bumpErr
	}
	for _, plan := range plans {
		clusters, clusterErr := eligibleClusters(
			ctx,
			qtx,
			s.clusterSilentAfter,
			workloadplatform.Platform(plan.service.GetPlatform()),
		)
		if clusterErr != nil {
			return clusterErr
		}
		targets := make([]placementTarget, 0, len(plan.source))
		for _, region := range plan.source {
			cluster, found := clusterForRegion(clusters, region.Region)
			if !found {
				return errNoActiveCluster.Detail("%s, requires %s", region.Region, plan.service.GetPlatform())
			}
			targets = append(targets, placementTarget{region: region.Region, clusterID: cluster.ID})
		}
		source := plan.source[0]
		if _, deployErr := createDeployment(ctx, qtx, &counts, newDeployment{
			params: genDb.CreateDeploymentParams{
				ResourceID:         res.ID,
				EnvironmentID:      environmentID,
				SecretNames:        source.SecretNames,
				ActorType:          principal.ActorType,
				ActorID:            principal.ActorID,
				Method:             principal.Method,
				CommitSha:          source.CommitSha,
				CommitDirty:        source.CommitDirty,
				Trigger:            genDb.DeploymentTriggerScale,
				SourceDeploymentID: &source.ID,
			},
			service: plan.service,
			targets: targets,
			render:  render.payload,
		}); deployErr != nil {
			return deployErr
		}
	}
	return events.Record(ctx, qtx, ev)
}

func validateScaleRequest(r *resourcev1.ScaleResourceRequest) error {
	if r.GetReplicas() == 0 && r.GetCpu() == "" && r.GetMemory() == "" {
		return errScaleNothingRequested
	}

	if cpu := r.GetCpu(); cpu != "" {
		if _, err := servicedefaults.ParseCPU(cpu); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
		}
	}

	if memory := r.GetMemory(); memory != "" {
		if _, err := servicedefaults.ParseMemory(memory); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(err)
		}
	}

	return nil
}

func scaleChangesDeployment(
	r *resourcev1.ScaleResourceRequest,
	target *deploymentv1.RegionDeploymentTarget,
	currentReplicas int32,
) bool {
	if r.GetCpu() != "" && r.GetCpu() != target.GetCpu() {
		return true
	}

	if r.GetMemory() != "" && r.GetMemory() != target.GetMemory() {
		return true
	}

	return r.GetReplicas() != 0 && (r.GetReplicas() != currentReplicas || target.GetScalers().GetEnabled())
}

func applyScale(r *resourcev1.ScaleResourceRequest, target *deploymentv1.RegionDeploymentTarget) {
	if cpu := r.GetCpu(); cpu != "" {
		target.Cpu = &cpu
	}
	if memory := r.GetMemory(); memory != "" {
		target.Memory = &memory
	}
	replicas := r.GetReplicas()
	if replicas == 0 {
		return
	}
	target.MinReplicas = &replicas
	target.MaxReplicas = &replicas
	target.Scalers = &deploymentv1.Scalers{}
}

// resourceDomainToListProto converts a slice of ResourceDomain to proto ResourceDomain list
func resourceDomainToListProto(domains []genDb.ResourceDomain) []*domainv1.ResourceDomain {
	protoDomains := make([]*domainv1.ResourceDomain, 0, len(domains))
	for _, d := range domains {
		domainSource := domainv1.DomainType_DOMAIN_TYPE_UNSPECIFIED
		if d.DomainSource == genDb.DomainSourcePlatformProvided {
			domainSource = domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED
		}

		domain := &domainv1.ResourceDomain{
			Id:           d.ID.String(),
			ResourceId:   d.ResourceID.String(),
			Domain:       d.Domain,
			DomainSource: domainSource,
			IsPrimary:    d.IsPrimary,
			CreatedAt:    timestamppb.New(d.CreatedAt),
			UpdatedAt:    timestamppb.New(d.UpdatedAt),
		}

		domain.SubdomainLabel = d.SubdomainLabel
		domain.PlatformDomain = d.PlatformDomain

		protoDomains = append(protoDomains, domain)
	}
	return protoDomains
}

// dbResourceToProto converts a database Resource to the proto Resource
// to be returned to client. Note: caller is responsible for fetching domains separately.
func dbResourceToProto(
	res genDb.Resource,
	statuses []*resourcev1.EnvironmentStatus,
	domains []genDb.ResourceDomain,
) (*resourcev1.Resource, error) {
	spec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		return nil, fmt.Errorf("decode spec of resource %s: %w", res.ID, err)
	}

	return &resourcev1.Resource{
		Id:                  res.ID.String(),
		WorkspaceId:         res.WorkspaceID.String(),
		Name:                res.Name,
		DisplayName:         res.DisplayName,
		Type:                resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:                spec,
		Domains:             resourceDomainToListProto(domains),
		CreatedBy:           res.CreatedBy.String(),
		CreatedAt:           timestamppb.New(res.CreatedAt),
		UpdatedAt:           timestamppb.New(res.UpdatedAt),
		Description:         &res.Description,
		Partial:             res.Partial,
		Revision:            res.Revision,
		EnvironmentStatuses: statuses,
	}, nil
}

// GetResourceEnv returns the plain env variables of the service's active deployment in the
// environment, read from its placement's desired spec, and the secret names the deployment
// declares. Secret values never appear there.
func (s *ResourceServer) GetResourceEnv(
	ctx context.Context,
	req *resourcev1.GetResourceEnvRequest,
) (*resourcev1.GetResourceEnvResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.GetResourceEnv, resourceID); err != nil {
		return nil, err
	}
	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get resource: %w", err)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, uuid.MustParse(req.GetEnvironmentId()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEnvironmentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != res.WorkspaceID {
		return nil, ErrEnvironmentNotFound
	}
	deployments, err := listLiveDeploymentsForEnvironment(ctx, s.queries, res.ID, env.ID)
	if err != nil {
		return nil, err
	}
	plain := map[string]string{}
	var secretNames []string
	for _, deployment := range deployments {
		vars, envErr := desiredEnv(ctx, s.queries, res.ID, deployment.EnvironmentID, deployment.ClusterID)
		if envErr != nil {
			return nil, fmt.Errorf("read the desired env: %w", envErr)
		}
		maps.Copy(plain, vars)
		secretNames = slices.Clone(deployment.SecretNames)
		slices.Sort(secretNames)
		break
	}
	return &resourcev1.GetResourceEnvResponse{Env: plain, SecretNames: secretNames}, nil
}

// validResourceName reports whether name can name a resource: a DNS label that is not reserved
// for private service discovery.
func validResourceName(name string) bool {
	return locoControllerV1.PrivateServiceName(name) != ""
}
