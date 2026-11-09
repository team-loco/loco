package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/api/timeutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpcerr"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

var (
	ErrResourceNotFound      = rpcerr.NewCoded(connect.CodeNotFound, "resource not found")
	ErrDomainNotFound        = rpcerr.NewCoded(connect.CodeNotFound, "domain not found")
	ErrResourceNameNotUnique = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"resource name already exists in this workspace",
	)
	errPrivateServiceName = rpcerr.NewCoded(connect.CodeInvalidArgument,
		"resource name needs a letter or digit, and its slug cannot be localhost or resource-<UUID>",
	)
	errSlugInUse = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"another resource in this workspace has the same slug",
	)
	ErrSubdomainNotAvailable = errors.New("subdomain already in use")
	ErrClusterNotFound       = errors.New("cluster not found")
	ErrClusterNotHealthy     = errors.New("cluster is not healthy")
	ErrInvalidResourceType   = errors.New("invalid resource type")

	errClusterHeldByEnvironment = errors.New(
		"each environment needs its own cluster, and this cluster already runs the service for another environment",
	)

	errDomainInUse          = rpcerr.NewCoded(connect.CodeAlreadyExists, "domain already in use")
	errUnknownResourceSpec  = rpcerr.NewCoded(connect.CodeInvalidArgument, "unknown resource spec type")
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
}

// NewResourceServer creates a new ResourceServer instance
func NewResourceServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	defaults servicedefaults.Defaults,
	clusterSilentAfter time.Duration,
) *ResourceServer {
	return &ResourceServer{
		db:                 db,
		queries:            queries,
		authz:              authz.New(db, queries),
		defaults:           defaults,
		clusterSilentAfter: clusterSilentAfter,
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
	if err = authorize(ctx, s.authz, actions.CreateResource, workspaceID); err != nil {
		return nil, err
	}

	slug := resourceSlug(req.GetName())
	if slug == "" {
		return nil, errPrivateServiceName
	}

	// validate that spec contains a service spec (for now, only services are supported)
	if req.GetSpec().GetService() == nil {
		return nil, errOnlyServiceResources
	}

	hasDomain := req.GetDomain() != nil
	var fullDomain string
	var subdomainLabel *string
	var platformDomain *string

	if hasDomain {
		fullDomain = req.GetDomain().GetSubdomain() + "." + req.GetDomain().GetPlatformDomain()
		platform, label, checkErr := s.checkNewResourceDomain(ctx, workspaceID, req.GetSpec(), fullDomain)
		if checkErr != nil {
			return nil, fmt.Errorf("check the resource domain: %w", checkErr)
		}
		subdomainLabel = &label
		platformDomain = &platform
	}

	specJSON, err := marshalResourceSpec(ctx, req.GetSpec())
	if err != nil {
		return nil, err
	}

	if req.GetType() != resourcev1.ResourceType_RESOURCE_TYPE_SERVICE {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).
			WithCause(ErrInvalidResourceType)
	}

	params := genDb.CreateResourceParams{
		WorkspaceID: workspaceID,
		Name:        req.GetName(),
		Slug:        slug,
		Type:        genDb.ResourceTypeService,
		Spec:        specJSON,
		SpecVersion: specVersion,
		Description: req.GetDescription(),
	}
	var resourceID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, workspaceID); lockErr != nil {
			return lockErr
		}
		if slugErr := checkSlugFree(ctx, qtx, workspaceID, slug, nil); slugErr != nil {
			return slugErr
		}
		var createErr error
		resourceID, createErr = qtx.CreateResource(ctx, params)
		if pgForeignKeyConstraint(createErr) == pgResourceWorkspaceReference {
			return ErrWorkspaceNotFound
		}
		if isUniqueViolation(createErr, pgResourceNameUnique) {
			return ErrResourceNameNotUnique
		}
		if isUniqueViolation(createErr, pgResourceSlugUnique) {
			return errSlugInUse
		}
		if createErr != nil {
			return fmt.Errorf("insert resource: %w", createErr)
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}
		if hasDomain {
			environmentID, envErr := workspaceProductionEnvironment(ctx, qtx, workspaceID)
			if envErr != nil {
				return envErr
			}
			_, domainErr := qtx.CreateResourceDomain(ctx, genDb.CreateResourceDomainParams{
				ResourceID:     resourceID,
				EnvironmentID:  environmentID,
				Domain:         fullDomain,
				DomainSource:   genDb.DomainSourcePlatformProvided,
				SubdomainLabel: subdomainLabel,
				PlatformDomain: platformDomain,
				IsPrimary:      true,
			})
			if isDomainTaken(domainErr) {
				return errDomainInUse
			}
			if domainErr != nil {
				return fmt.Errorf("insert resource domain: %w", domainErr)
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

func (s *ResourceServer) checkNewResourceDomain(
	ctx context.Context,
	workspaceID uuid.UUID,
	spec *resourcev1.ResourceSpec,
	hostname string,
) (string, string, error) {
	env, err := s.queries.GetWorkspaceProductionEnvironment(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errNoProductionEnvironment
	}
	if err != nil {
		return "", "", fmt.Errorf("get production environment: %w", err)
	}
	regions := enabledRegions(spec.GetService().GetRegions())
	check, err := resourceDomainCheck(ctx, s.queries, env.EnvironmentType, regions, nil, s.clusterSilentAfter)
	if err != nil {
		return "", "", fmt.Errorf("list domain clusters: %w", err)
	}
	return configplan.CheckDomain(hostname, check.clusters, check.targets)
}

func marshalResourceSpec(ctx context.Context, spec *resourcev1.ResourceSpec) ([]byte, error) {
	var (
		specJSON []byte
		err      error
	)
	switch specType := spec.GetSpec().(type) {
	case *resourcev1.ResourceSpec_Service:
		specJSON, err = protojson.Marshal(specType.Service)
	case *resourcev1.ResourceSpec_Database:
		specJSON, err = protojson.Marshal(specType.Database)
	case *resourcev1.ResourceSpec_Cache:
		specJSON, err = protojson.Marshal(specType.Cache)
	case *resourcev1.ResourceSpec_Queue:
		specJSON, err = protojson.Marshal(specType.Queue)
	case *resourcev1.ResourceSpec_Blob:
		specJSON, err = protojson.Marshal(specType.Blob)
	default:
		return nil, errUnknownResourceSpec
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal resource spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidSpec).WithCause(err)
	}
	return specJSON, nil
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

	message := req.ProtoReflect()
	fields := message.Descriptor().Fields()
	rename := message.Has(fields.ByName(updateNameField))
	updateParams := genDb.UpdateResourceParams{ID: resourceID}
	if rename {
		slug := resourceSlug(req.GetName())
		if slug == "" {
			return nil, errPrivateServiceName
		}
		updateParams.Name = new(req.GetName())
		updateParams.Slug = &slug
	}
	if message.Has(fields.ByName(updateDescriptionField)) {
		updateParams.Description = new(req.GetDescription())
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceEnvironments(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		resource, getErr := qtx.GetResourceByID(ctx, resourceID)
		if getErr != nil {
			return fmt.Errorf("get resource: %w", getErr)
		}
		if rename {
			slugErr := checkSlugFree(ctx, qtx, resource.WorkspaceID, *updateParams.Slug, &resourceID)
			if slugErr != nil {
				return slugErr
			}
		}
		_, updateErr := qtx.UpdateResource(ctx, updateParams)
		if isUniqueViolation(updateErr, pgResourceNameUnique) {
			return ErrResourceNameNotUnique
		}
		if isUniqueViolation(updateErr, pgResourceSlugUnique) {
			return errSlugInUse
		}
		if updateErr != nil {
			return fmt.Errorf("update resource: %w", updateErr)
		}
		if rename {
			resource.Name = req.GetName()
			resource.Slug = *updateParams.Slug
			if renameErr := renamePlacements(ctx, qtx, resource); renameErr != nil {
				return renameErr
			}
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

// ListRegions lists healthy and degraded regions for an environment.
func (s *ResourceServer) ListRegions(
	ctx context.Context,
	req *resourcev1.ListRegionsRequest,
) (*resourcev1.ListRegionsResponse, error) {
	environmentID := uuid.MustParse(req.GetEnvironmentId())
	env, err := visibleEnvironment(ctx, s.queries, s.authz, environmentID, actions.ListRegions)
	if err != nil {
		return nil, err
	}
	clusters, err := s.queries.ListClusterRegions(ctx, genDb.ListClusterRegionsParams{
		Tier:       env.EnvironmentType,
		HeardSince: time.Now().Add(-s.clusterSilentAfter),
	})
	if err != nil {
		return nil, fmt.Errorf("list cluster regions: %w", err)
	}
	regions := make([]*resourcev1.RegionInfo, 0, len(clusters))
	for _, cluster := range clusters {
		regions = append(regions, &resourcev1.RegionInfo{
			Region:       cluster.Region,
			IsDefault:    cluster.IsDefault,
			HealthStatus: derefString(cluster.HealthStatus),
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
		if rolloutErr := ensureNoRollout(ctx, qtx, resourceID, env.ID, req.GetRegion()); rolloutErr != nil {
			return rolloutErr
		}
		return s.scaleCurrentDeployments(ctx, qtx, currentResource, env.ID, req)
	})
	if err != nil {
		return nil, deploymentTxError(ctx, err)
	}
	return &resourcev1.ScaleResourceResponse{}, nil
}

func (s *ResourceServer) scaleCurrentDeployments(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	environmentID uuid.UUID,
	req *resourcev1.ScaleResourceRequest,
) error {
	resourceID := res.ID
	currentByRegion, err := activeDeploymentsForRegions(ctx, qtx, resourceID, environmentID, req.GetRegion())
	if err != nil {
		return err
	}

	plans := make([]regionRedeploy, 0, len(currentByRegion))
	for _, current := range currentByRegion {
		serviceDeploymentSpec, specErr := currentServiceSpec(ctx, current, res.Type)
		if specErr != nil {
			return specErr
		}

		if !scaleChangesDeployment(req, serviceDeploymentSpec, current.Replicas) {
			continue
		}

		plainEnv, envErr := desiredEnv(ctx, qtx, resourceID, current.ClusterID)
		if envErr != nil {
			return fmt.Errorf("read scaling environment: %w", envErr)
		}
		serviceDeploymentSpec.Env = plainEnv
		replicas := applyScale(req, serviceDeploymentSpec, current.Replicas)
		plan, planErr := s.planRegionRedeploy(
			ctx,
			qtx,
			current,
			serviceDeploymentSpec,
			replicas,
			"Scheduled scaling event.",
		)
		if planErr != nil {
			return planErr
		}
		plans = append(plans, plan)
	}

	if len(plans) == 0 {
		return errScaleUnchanged
	}

	scaled := map[string]any{events.FieldEnvironmentID: environmentID.String()}
	if req.Replicas != nil {
		scaled["replicas"] = req.GetReplicas()
	}
	if req.Cpu != nil {
		scaled["cpu"] = req.GetCpu()
	}
	if req.Memory != nil {
		scaled["memory"] = req.GetMemory()
	}
	if req.Region != nil {
		scaled["region"] = req.GetRegion()
	}
	scaledEvent := events.Event{
		Type:        events.ResourceScaled,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
		Data:        scaled,
	}
	return s.redeployRegions(ctx, qtx, res, plans, scaledEvent)
}

type regionRedeploy struct {
	params          genDb.CreateDeploymentParams
	deploymentSpec  *deploymentv1.DeploymentSpec
	environmentName string
}

var (
	errNoDeploymentInRegion = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"resource has no active deployment in region",
	)
	errPreviousDeploymentNoSpec = rpcerr.NewCoded(connect.CodeInvalidArgument, "previous deployment has no spec")
)

func activeDeploymentsForRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	resourceID uuid.UUID,
	environmentID uuid.UUID,
	requestedRegion string,
) ([]genDb.Deployment, error) {
	deploymentList, err := qtx.ListActiveDeploymentsForResource(ctx, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list active deployments: %w", err)
	}

	activeByRegion := make(map[string]genDb.Deployment, len(deploymentList))
	for _, d := range deploymentList {
		if d.EnvironmentID != environmentID {
			continue
		}
		if _, seen := activeByRegion[d.Region]; !seen {
			activeByRegion[d.Region] = d
		}
	}

	if requestedRegion != "" {
		d, found := activeByRegion[requestedRegion]
		if !found {
			return nil, errNoDeploymentInRegion.Detail("%s", requestedRegion)
		}
		return []genDb.Deployment{d}, nil
	}

	if len(activeByRegion) == 0 {
		return nil, errNoActiveDeployment
	}

	current := make([]genDb.Deployment, 0, len(activeByRegion))
	for _, region := range slices.Sorted(maps.Keys(activeByRegion)) {
		current = append(current, activeByRegion[region])
	}
	return current, nil
}

func currentServiceSpec(
	ctx context.Context,
	current genDb.Deployment,
	resourceType genDb.ResourceType,
) (*deploymentv1.ServiceDeploymentSpec, error) {
	if len(current.Spec) == 0 {
		return nil, errPreviousDeploymentNoSpec
	}

	deploymentSpec, err := converter.DeserializeDeploymentSpec(current.Spec, resourceType)
	if err != nil {
		slog.ErrorContext(ctx, "failed to deserialize deployment spec", "error", err)
		return nil, &invalidInputError{public: publicInvalidSpec, err: err}
	}

	serviceDeploymentSpec := deploymentSpec.GetService()
	if serviceDeploymentSpec == nil {
		return nil, errOnlyServiceResources
	}

	return serviceDeploymentSpec, nil
}

func (s *ResourceServer) planRegionRedeploy(
	ctx context.Context,
	qtx *genDb.Queries,
	current genDb.Deployment,
	serviceDeploymentSpec *deploymentv1.ServiceDeploymentSpec,
	replicas int32,
	message string,
) (regionRedeploy, error) {
	specJSON, err := deploymentSpecJSON(serviceDeploymentSpec)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal service deployment spec", "error", err)
		return regionRedeploy{}, &invalidInputError{public: publicInvalidSpec, err: err}
	}

	deploymentEnv, err := qtx.GetEnvironmentByID(ctx, current.EnvironmentID)
	if err != nil {
		return regionRedeploy{}, fmt.Errorf("get deployment environment: %w", err)
	}

	cluster, err := eligibleCluster(ctx, qtx, current.Region, deploymentEnv.EnvironmentType, s.clusterSilentAfter)
	if err != nil {
		return regionRedeploy{}, err
	}

	return regionRedeploy{
		params: genDb.CreateDeploymentParams{
			ResourceID:    current.ResourceID,
			ClusterID:     cluster.ID,
			Region:        current.Region,
			Replicas:      replicas,
			Status:        genDb.DeploymentStatusPending,
			IsActive:      true,
			Message:       message,
			Spec:          specJSON,
			SpecVersion:   specVersion,
			EnvironmentID: current.EnvironmentID,
			SecretNames:   current.SecretNames,
		},
		deploymentSpec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{
				Service: serviceDeploymentSpec,
			},
		},
		environmentName: deploymentEnv.Name,
	}, nil
}

func (s *ResourceServer) redeployRegions(
	ctx context.Context,
	qtx *genDb.Queries,
	res genDb.Resource,
	plans []regionRedeploy,
	ev events.Event,
) error {
	hostnames := make(map[uuid.UUID]string, len(plans))
	for _, plan := range plans {
		environmentID := plan.params.EnvironmentID
		if _, found := hostnames[environmentID]; found {
			continue
		}
		hostname, hostnameErr := primaryHostname(ctx, qtx, res.ID, environmentID)
		if hostnameErr != nil {
			return fmt.Errorf("get primary hostname: %w", hostnameErr)
		}
		hostnames[environmentID] = hostname
	}

	organizationID, err := qtx.GetOrganizationIDByWorkspaceID(ctx, res.WorkspaceID)
	if err != nil {
		return fmt.Errorf("get workspace organization: %w", err)
	}

	resourceSpec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		return fmt.Errorf("%w: decode resource spec: %w", errDesiredSpec, err)
	}

	bumped := make(map[uuid.UUID]bool, len(plans))
	for _, plan := range plans {
		environmentID := plan.params.EnvironmentID
		if !bumped[environmentID] {
			if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
				return bumpErr
			}
			bumped[environmentID] = true
		}
		buildSpec := desiredApplicationSpec(
			res,
			organizationID,
			resourceSpec,
			hostnames[plan.params.EnvironmentID],
			plan.deploymentSpec,
			plan.params.Region,
			plan.params.EnvironmentID,
			plan.environmentName,
			s.defaults,
		)
		if _, deployErr := createDeploymentWithCleanup(ctx, qtx, plan.params, buildSpec); deployErr != nil {
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
	spec *deploymentv1.ServiceDeploymentSpec,
	currentReplicas int32,
) bool {
	if r.GetCpu() != "" && r.GetCpu() != spec.GetCpu() {
		return true
	}

	if r.GetMemory() != "" && r.GetMemory() != spec.GetMemory() {
		return true
	}

	return r.GetReplicas() != 0 && r.GetReplicas() != currentReplicas
}

func applyScale(
	r *resourcev1.ScaleResourceRequest,
	spec *deploymentv1.ServiceDeploymentSpec,
	currentReplicas int32,
) int32 {
	if cpu := r.GetCpu(); cpu != "" {
		spec.Cpu = &cpu
	}
	if memory := r.GetMemory(); memory != "" {
		spec.Memory = &memory
	}
	replicas := r.GetReplicas()
	if replicas == 0 {
		return currentReplicas
	}
	spec.MinReplicas = &replicas
	if spec.GetMaxReplicas() < replicas {
		spec.MaxReplicas = &replicas
	}
	return replicas
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
			CreatedAt:    timeutil.ParsePostgresTimestamp(d.CreatedAt),
			UpdatedAt:    timeutil.ParsePostgresTimestamp(d.UpdatedAt),
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
		Slug:                res.Slug,
		Type:                resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:                spec,
		Domains:             resourceDomainToListProto(domains),
		CreatedAt:           timeutil.ParsePostgresTimestamp(res.CreatedAt),
		UpdatedAt:           timeutil.ParsePostgresTimestamp(res.UpdatedAt),
		Description:         &res.Description,
		Partial:             res.Partial,
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
	deployments, err := s.queries.ListActiveDeploymentsForResource(ctx, res.ID)
	if err != nil {
		return nil, fmt.Errorf("list active deployments: %w", err)
	}
	plain := map[string]string{}
	var secretNames []string
	for _, deployment := range deployments {
		if deployment.EnvironmentID != env.ID {
			continue
		}
		vars, envErr := desiredEnv(ctx, s.queries, res.ID, deployment.ClusterID)
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

// resourceSlug is the DNS label a resource's private Service and generated subdomain use, or ""
// when the name has none or it is reserved.
func resourceSlug(name string) string {
	return locoControllerV1.PrivateServiceName(locofile.Slug(name))
}

// checkSlugFree returns errSlugInUse naming the workspace resource, other than except, that
// already holds slug.
func checkSlugFree(
	ctx context.Context,
	qtx *genDb.Queries,
	workspaceID uuid.UUID,
	slug string,
	except *uuid.UUID,
) error {
	params := genDb.GetResourceNameBySlugParams{WorkspaceID: workspaceID, Slug: slug, ExceptID: except}
	other, err := qtx.GetResourceNameBySlug(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up slug %s: %w", slug, err)
	}
	return slugTakenError(slug, other)
}

func slugTakenError(slug, holder string) error {
	return errSlugInUse.Detail("%s is used by %q", slug, holder)
}
