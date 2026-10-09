package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/timeutil"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var (
	ErrResourceNotFound      = errors.New("resource not found")
	ErrDomainNotFound        = errors.New("domain not found")
	ErrResourceNameNotUnique = errors.New("resource name already exists in this workspace")
	ErrSubdomainNotAvailable = errors.New("subdomain already in use")
	ErrClusterNotFound       = errors.New("cluster not found")
	ErrClusterNotHealthy     = errors.New("cluster is not healthy")
	ErrInvalidResourceType   = errors.New("invalid resource type")

	errDomainInUse           = errors.New("domain already in use")
	errUnknownResourceSpec   = errors.New("unknown resource spec type")
	errOnlyServiceResources  = errors.New("only service resources are currently supported")
	errScaleNothingRequested = errors.New("at least one of replicas, cpu, or memory must be provided")
)

// protoResourceTypeToDb converts a proto ResourceType to a database ResourceType
func protoResourceTypeToDb(rt resourcev1.ResourceType) (genDb.ResourceType, error) {
	switch rt {
	case resourcev1.ResourceType_RESOURCE_TYPE_SERVICE:
		return genDb.ResourceTypeService, nil
	case resourcev1.ResourceType_RESOURCE_TYPE_DATABASE:
		return genDb.ResourceTypeDatabase, nil
	case resourcev1.ResourceType_RESOURCE_TYPE_CACHE:
		return genDb.ResourceTypeCache, nil
	case resourcev1.ResourceType_RESOURCE_TYPE_QUEUE:
		return genDb.ResourceTypeQueue, nil
	case resourcev1.ResourceType_RESOURCE_TYPE_BLOB:
		return genDb.ResourceTypeBlob, nil
	case resourcev1.ResourceType_RESOURCE_TYPE_FUNCTION:
		return genDb.ResourceTypeService, nil
	default:
		return "", ErrInvalidResourceType
	}
}

type ResourceServer struct {
	resourcev1connect.UnimplementedResourceServiceHandler
	db       *pgxpool.Pool
	queries  genDb.Querier
	authz    *authz.Authorizer
	defaults servicedefaults.Defaults
}

// NewResourceServer creates a new ResourceServer instance
func NewResourceServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	defaults servicedefaults.Defaults,
) *ResourceServer {
	return &ResourceServer{
		db:       db,
		queries:  queries,
		authz:    authz.New(db, queries),
		defaults: defaults,
	}
}

// CreateResource creates a new resource
func (s *ResourceServer) CreateResource(
	ctx context.Context,
	req *connect.Request[resourcev1.CreateResourceRequest],
) (*connect.Response[resourcev1.CreateResourceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateResource, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create resource", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// validate that spec contains a service spec (for now, only services are supported)
	if r.GetSpec().GetService() == nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errOnlyServiceResources,
		)
	}

	serviceSpec := r.GetSpec().GetService()

	hasDomain := r.GetDomain() != nil
	domainSource := genDb.DomainSourceUserProvided
	var fullDomain string
	var subdomainLabel *string
	var platformDomainID *uuid.UUID

	if hasDomain {
		if r.GetDomain().GetDomainSource() == domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED {
			domainSource = genDb.DomainSourcePlatformProvided
			parsedPlatformDomainID := uuid.MustParse(r.GetDomain().GetPlatformDomainId())
			platformDomainID = &parsedPlatformDomainID

			platformDomain, err := s.queries.GetPlatformDomain(ctx, parsedPlatformDomainID)
			if err != nil {
				slog.ErrorContext(ctx, "failed to get platform domain", "error", err)
				return nil, connect.NewError(connect.CodeInvalidArgument, ErrPlatformDomainNotFound)
			}

			fullDomain = r.GetDomain().GetSubdomain() + "." + platformDomain.Domain
			subdomain := r.GetDomain().GetSubdomain()
			subdomainLabel = &subdomain
		} else {
			fullDomain = r.GetDomain().GetDomain()
		}

		available, err := s.queries.CheckDomainAvailability(ctx, fullDomain)
		if err != nil {
			slog.ErrorContext(ctx, "failed to check domain availability", "domain", fullDomain, "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}

		if !available {
			slog.WarnContext(ctx, "domain already in use", "domain", fullDomain)
			return nil, connect.NewError(connect.CodeAlreadyExists, errDomainInUse)
		}
	}

	specJSON, err := marshalResourceSpec(ctx, r.GetSpec())
	if err != nil {
		return nil, err
	}

	resourceType, err := protoResourceTypeToDb(r.GetType())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	workspaceID := uuid.MustParse(r.GetWorkspaceId())

	params := genDb.CreateResourceParams{
		WorkspaceID: workspaceID,
		Name:        r.GetName(),
		Type:        resourceType,
		Status:      genDb.ResourceStatusUnavailable,
		Spec:        specJSON,
		SpecVersion: int32(1),
		Description: r.GetDescription(),
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	if lockErr := lockWorkspaceEnvironments(ctx, qtx, workspaceID); lockErr != nil {
		slog.ErrorContext(ctx, "failed to lock environments", "error", lockErr)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceID, err := qtx.CreateResource(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create resource", "error", err)
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(
				connect.CodeAlreadyExists,
				errors.New("a resource with this name already exists in this workspace"),
			)
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create resource"))
	}

	if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
		slog.ErrorContext(ctx, "failed to bump environment revisions", "error", bumpErr)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	for region, regionConfig := range serviceSpec.GetRegions() {
		isPrimary := regionConfig.GetPrimary()
		_, regionErr := qtx.CreateResourceRegion(ctx, genDb.CreateResourceRegionParams{
			ResourceID: resourceID,
			Region:     region,
			IsPrimary:  isPrimary,
			Status:     genDb.RegionIntentStatusDesired,
		})
		if regionErr != nil {
			slog.ErrorContext(ctx, "failed to create resource region", "error", regionErr)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
	}

	if hasDomain {
		domainParams := genDb.CreateResourceDomainParams{
			ResourceID:       resourceID,
			Domain:           fullDomain,
			DomainSource:     domainSource,
			SubdomainLabel:   subdomainLabel,
			PlatformDomainID: platformDomainID,
			IsPrimary:        true,
		}

		_, err = qtx.CreateResourceDomain(ctx, domainParams)
		if err != nil {
			slog.ErrorContext(ctx, "failed to create resource domain", "error", err)
			if isPgConstraintViolation(err) {
				return nil, connect.NewError(connect.CodeAlreadyExists, errDomainInUse)
			}
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.ResourceCreated,
		WorkspaceID: new(workspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
		Data:        map[string]any{events.FieldName: r.GetName()},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record resource creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit resource creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.CreateResourceResponse{ResourceId: resourceID.String()}), nil
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
		return nil, connect.NewError(connect.CodeInvalidArgument, errUnknownResourceSpec)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal resource spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}
	return specJSON, nil
}

// GetResource retrieves a resource by ID
func (s *ResourceServer) GetResource(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceRequest],
) (*connect.Response[resourcev1.GetResourceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	var resourceIDStr string
	switch key := r.GetKey().(type) {
	case *resourcev1.GetResourceRequest_ResourceId:
		resourceIDStr = key.ResourceId
	case *resourcev1.GetResourceRequest_NameKey:
		nameKey := key.NameKey
		named, lookupErr := s.resourceIDByName(ctx, scopes, nameKey)
		if lookupErr != nil {
			return nil, lookupErr
		}
		resourceIDStr = named.String()
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("resource_id or name_key is required"))
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetResource, resourceIDStr),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get resource", "resourceId", resourceIDStr)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID, err := uuid.Parse(resourceIDStr)
	if err != nil {
		slog.ErrorContext(ctx, "invalid resource id format", "resourceId", resourceIDStr)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource id: %w", err))
	}

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "id", resourceIDStr)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, res.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, res.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.GetResourceResponse{
		Resource: dbResourceToProto(res, resourceDomains, resourceRegions),
	}), nil
}

func (s *ResourceServer) resourceIDByName(
	ctx context.Context,
	scopes []genDb.EntityScope,
	nameKey *resourcev1.GetResourceNameKey,
) (uuid.UUID, error) {
	rawWorkspaceID := nameKey.GetWorkspaceId()
	workspaceID, err := uuid.Parse(rawWorkspaceID)
	if err != nil {
		invalid := fmt.Errorf("invalid workspace id: %w", err)
		return uuid.UUID{}, connect.NewError(connect.CodeInvalidArgument, invalid)
	}
	name := nameKey.GetName()
	res, err := s.queries.GetResourceByNameAndWorkspace(ctx, genDb.GetResourceByNameAndWorkspaceParams{
		WorkspaceID: workspaceID,
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		workspaceRead := genDb.EntityScope{
			EntityType: genDb.EntityTypeWorkspace,
			EntityID:   workspaceID,
			Scope:      genDb.ScopeRead,
		}
		if verifyErr := s.authz.Check(ctx, scopes, workspaceRead); verifyErr != nil {
			slog.WarnContext(ctx, "unauthorized to look up a resource by name", "workspaceId", workspaceID)
			return uuid.UUID{}, connect.NewError(connect.CodePermissionDenied, verifyErr)
		}
		return uuid.UUID{}, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up resource by name", "error", err, "workspaceId", workspaceID)
		return uuid.UUID{}, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return res.ID, nil
}

// ListWorkspaceResources lists all resources in a workspace
func (s *ResourceServer) ListWorkspaceResources(
	ctx context.Context,
	req *connect.Request[resourcev1.ListWorkspaceResourcesRequest],
) (*connect.Response[resourcev1.ListWorkspaceResourcesResponse], error) {
	r := req.Msg

	slog.InfoContext(ctx, "received req to list resources", "workspaceId", r.GetWorkspaceId())

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListResources, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list resources", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken *string
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = &cursorID
	}

	wsID := uuid.MustParse(r.GetWorkspaceId())

	dbResources, err := s.queries.ListResourcesForWorkspace(ctx, genDb.ListResourcesForWorkspaceParams{
		WorkspaceID: wsID,
		Limit:       pageSize,
		PageToken:   pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resources", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceIDs := make([]uuid.UUID, len(dbResources))
	for i, dbResource := range dbResources {
		resourceIDs[i] = dbResource.ID
	}

	allDomains, err := s.queries.ListResourceDomainsForResources(ctx, resourceIDs)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	allRegions, err := s.queries.ListResourceRegionsForResources(ctx, resourceIDs)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	domainsByResource := make(map[uuid.UUID][]genDb.ResourceDomain, len(dbResources))
	for _, domain := range allDomains {
		domainsByResource[domain.ResourceID] = append(domainsByResource[domain.ResourceID], domain)
	}
	regionsByResource := make(map[uuid.UUID][]genDb.ResourceRegion, len(dbResources))
	for _, region := range allRegions {
		regionsByResource[region.ResourceID] = append(regionsByResource[region.ResourceID], region)
	}

	resources := make([]*resourcev1.Resource, 0, len(dbResources))
	for _, dbResource := range dbResources {
		resourceDomains := domainsByResource[dbResource.ID]
		resourceRegions := regionsByResource[dbResource.ID]
		resources = append(resources, dbResourceToProto(dbResource, resourceDomains, resourceRegions))
	}

	var nextPageToken string
	if len(dbResources) == int(pageSize) {
		nextPageToken = encodeCursor(dbResources[len(dbResources)-1].ID.String())
	}

	return connect.NewResponse(&resourcev1.ListWorkspaceResourcesResponse{
		Resources:     resources,
		NextPageToken: nextPageToken,
	}), nil
}

// UpdateResource updates a resource
func (s *ResourceServer) UpdateResource(
	ctx context.Context,
	req *connect.Request[resourcev1.UpdateResourceRequest],
) (*connect.Response[resourcev1.UpdateResourceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateResource, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

	updateParams := genDb.UpdateResourceParams{
		ID: resourceID,
	}

	if r.GetName() != "" {
		n := r.GetName()
		updateParams.Name = &n
	}

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceEnvironments(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if _, updateErr := qtx.UpdateResource(ctx, updateParams); updateErr != nil {
			return updateErr
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
		slog.ErrorContext(ctx, "failed to update resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.UpdateResourceResponse{ResourceId: r.GetResourceId()}), nil
}

// DeleteResource deletes a resource
func (s *ResourceServer) DeleteResource(
	ctx context.Context,
	req *connect.Request[resourcev1.DeleteResourceRequest],
) (*connect.Response[resourcev1.DeleteResourceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteResource, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockWorkspaceEnvironments(ctx, qtx, res.WorkspaceID); lockErr != nil {
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
		slog.ErrorContext(ctx, "failed to delete resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.DeleteResourceResponse{}), nil
}

// GetResourceStatus retrieves a resource and its current deployment status
func (s *ResourceServer) GetResourceStatus(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceStatusRequest],
) (*connect.Response[resourcev1.GetResourceStatusResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetResourceStatus, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get resource status", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: resourceID,
		Limit:      1,
		PageToken:  nil, // empty for first page
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var deploymentStatus *resourcev1.DeploymentStatus
	if len(deploymentList) > 0 {
		deployment := deploymentList[0]
		deploymentStatus = &resourcev1.DeploymentStatus{
			Id:       deployment.ID.String(),
			Status:   deploymentStatusToProto(deployment.Status),
			Replicas: deployment.Replicas,
			Message:  &deployment.Message,
		}
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, res.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, res.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.GetResourceStatusResponse{
		Resource:          dbResourceToProto(res, resourceDomains, resourceRegions),
		CurrentDeployment: deploymentStatus,
	}), nil
}

// ListRegions lists available regions for resource deployment
func (s *ResourceServer) ListRegions(
	ctx context.Context,
	_ *connect.Request[resourcev1.ListRegionsRequest],
) (*connect.Response[resourcev1.ListRegionsResponse], error) {
	clusters, err := s.queries.ListClustersActive(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list clusters", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	regionMap := make(map[string]*resourcev1.RegionInfo)
	for _, cluster := range clusters {
		if _, exists := regionMap[cluster.Region]; !exists {
			regionMap[cluster.Region] = &resourcev1.RegionInfo{
				Region:       cluster.Region,
				IsDefault:    cluster.IsDefault,
				HealthStatus: derefString(cluster.HealthStatus),
			}
		}
	}

	var protoRegions []*resourcev1.RegionInfo
	for _, info := range regionMap {
		protoRegions = append(protoRegions, info)
	}

	return connect.NewResponse(&resourcev1.ListRegionsResponse{
		Regions: protoRegions,
	}), nil
}

// ScaleResource scales a resource by creating a new deployment with updated resources
func (s *ResourceServer) ScaleResource(
	ctx context.Context,
	req *connect.Request[resourcev1.ScaleResourceRequest],
) (*connect.Response[resourcev1.ScaleResourceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ScaleResource, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to scale resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if err := validateScaleRequest(ctx, r); err != nil {
		return nil, err
	}
	resourceID := uuid.MustParse(r.GetResourceId())

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	currentByRegion, err := s.activeDeploymentsForRegions(ctx, resourceID, r.GetRegion())
	if err != nil {
		return nil, err
	}

	plans := make([]regionRedeploy, 0, len(currentByRegion))
	for _, current := range currentByRegion {
		serviceDeploymentSpec, specErr := currentServiceSpec(ctx, current, res.Type)
		if specErr != nil {
			return nil, specErr
		}

		if !scaleChangesDeployment(r, serviceDeploymentSpec, current.Replicas) {
			continue
		}

		replicas := applyScale(r, serviceDeploymentSpec, current.Replicas)
		plan, planErr := s.planRegionRedeploy(ctx, current, serviceDeploymentSpec, replicas, "Scheduled scaling event.")
		if planErr != nil {
			return nil, planErr
		}
		envSourceCluster := current.ClusterID
		plan.envSourceCluster = &envSourceCluster
		plans = append(plans, plan)
	}

	if len(plans) == 0 {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("scaling values must be different from current deployment"),
		)
	}

	scaled := map[string]any{}
	if r.Replicas != nil {
		scaled["replicas"] = r.GetReplicas()
	}
	if r.Cpu != nil {
		scaled["cpu"] = r.GetCpu()
	}
	if r.Memory != nil {
		scaled["memory"] = r.GetMemory()
	}
	if r.Region != nil {
		scaled["region"] = r.GetRegion()
	}
	scaledEvent := events.Event{
		Type:        events.ResourceScaled,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
		Data:        scaled,
	}
	if err := s.redeployRegions(ctx, res, plans, scaledEvent); err != nil {
		return nil, err
	}
	return connect.NewResponse(&resourcev1.ScaleResourceResponse{}), nil
}

// UpdateResourceEnv updates environment variables for a resource
func (s *ResourceServer) UpdateResourceEnv(
	ctx context.Context,
	req *connect.Request[resourcev1.UpdateResourceEnvRequest],
) (*connect.Response[resourcev1.UpdateResourceEnvResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateResourceEnv, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update resource env", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if len(r.GetEnv()) == 0 {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("at least one environment variable must be provided"),
		)
	}

	resourceID := uuid.MustParse(r.GetResourceId())

	res, err := s.queries.GetResourceByID(ctx, resourceID)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	currentByRegion, err := s.activeDeploymentsForRegions(ctx, resourceID, r.GetRegion())
	if err != nil {
		return nil, err
	}

	plans := make([]regionRedeploy, 0, len(currentByRegion))
	for _, current := range currentByRegion {
		serviceDeploymentSpec, specErr := currentServiceSpec(ctx, current, res.Type)
		if specErr != nil {
			return nil, specErr
		}

		serviceDeploymentSpec.Env = r.GetEnv()

		plan, planErr := s.planRegionRedeploy(
			ctx,
			current,
			serviceDeploymentSpec,
			current.Replicas,
			"Scheduled environment update",
		)
		if planErr != nil {
			return nil, planErr
		}
		plans = append(plans, plan)
	}

	envKeys := slices.Sorted(maps.Keys(r.GetEnv()))
	envEvent := events.Event{
		Type:        events.ResourceEnvUpdated,
		WorkspaceID: new(res.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(resourceID),
		Data:        map[string]any{"keys": envKeys},
	}
	if err := s.redeployRegions(ctx, res, plans, envEvent); err != nil {
		return nil, err
	}
	return connect.NewResponse(&resourcev1.UpdateResourceEnvResponse{}), nil
}

type regionRedeploy struct {
	params           genDb.CreateDeploymentParams
	deploymentSpec   *deploymentv1.DeploymentSpec
	environmentName  string
	envSourceCluster *uuid.UUID
}

func (s *ResourceServer) activeDeploymentsForRegions(
	ctx context.Context,
	resourceID uuid.UUID,
	requestedRegion string,
) ([]genDb.Deployment, error) {
	resourceRegions, err := s.queries.ListResourceRegions(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	regions, err := selectRegionsToScale(requestedRegion, resourceRegions)
	if err != nil {
		return nil, err
	}

	deploymentList, err := s.queries.ListActiveDeploymentsForResource(ctx, resourceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	activeByRegion := make(map[string]genDb.Deployment, len(deploymentList))
	for _, d := range deploymentList {
		if _, seen := activeByRegion[d.Region]; !seen {
			activeByRegion[d.Region] = d
		}
	}

	current := make([]genDb.Deployment, 0, len(regions))
	for _, region := range regions {
		d, found := activeByRegion[region]
		if !found {
			continue
		}
		current = append(current, d)
	}

	if len(current) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no active deployment found for resource"))
	}

	return current, nil
}

func currentServiceSpec(
	ctx context.Context,
	current genDb.Deployment,
	resourceType genDb.ResourceType,
) (*deploymentv1.ServiceDeploymentSpec, error) {
	if len(current.Spec) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("previous deployment has no spec"))
	}

	deploymentSpec, err := converter.DeserializeDeploymentSpec(current.Spec, string(resourceType))
	if err != nil {
		slog.ErrorContext(ctx, "failed to deserialize deployment spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	serviceDeploymentSpec := deploymentSpec.GetService()
	if serviceDeploymentSpec == nil {
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			errOnlyServiceResources,
		)
	}

	return serviceDeploymentSpec, nil
}

func (s *ResourceServer) planRegionRedeploy(
	ctx context.Context,
	current genDb.Deployment,
	serviceDeploymentSpec *deploymentv1.ServiceDeploymentSpec,
	replicas int32,
	message string,
) (regionRedeploy, error) {
	clonedSpec := proto.Clone(serviceDeploymentSpec)
	specForDB, ok := clonedSpec.(*deploymentv1.ServiceDeploymentSpec)
	if !ok {
		slog.ErrorContext(ctx, "failed to clone service deployment spec")
		return regionRedeploy{}, connect.NewError(connect.CodeInternal, errCloneServiceSpec)
	}
	specForDB.Env = nil

	specJSON, err := protojson.Marshal(specForDB)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal service deployment spec", "error", err)
		return regionRedeploy{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	deploymentEnv, err := s.queries.GetEnvironmentByID(ctx, current.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment for deployment", "error", err)
		return regionRedeploy{}, connect.NewError(connect.CodeInternal, ErrDB)
	}

	cluster, err := s.queries.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
		Region: current.Region,
		Tier:   deploymentEnv.EnvironmentType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster for region", "region", current.Region, "error", err)
		return regionRedeploy{}, connect.NewError(
			connect.CodeInternal,
			fmt.Errorf("no active cluster available for region %s", current.Region),
		)
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
			SpecVersion:   int32(1),
			EnvironmentID: current.EnvironmentID,
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
	res genDb.Resource,
	plans []regionRedeploy,
	ev events.Event,
) error {
	hostname, err := primaryHostname(ctx, s.queries, res.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get the resource's primary domain", "resourceId", res.ID, "error", err)
		return connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceSpec, err := converter.DeserializeResourceSpecByType(res.Spec, string(res.Type))
	if err != nil {
		slog.ErrorContext(ctx, "failed to deserialize resource spec", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", err))
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockWorkspaceEnvironments(ctx, qtx, res.WorkspaceID); lockErr != nil {
			return lockErr
		}
		bumped := make(map[uuid.UUID]bool, len(plans))
		for _, plan := range plans {
			if inheritErr := inheritDesiredEnv(ctx, qtx, plan); inheritErr != nil {
				return inheritErr
			}
			environmentID := plan.params.EnvironmentID
			if !bumped[environmentID] {
				if bumpErr := bumpEnvironmentRevision(ctx, qtx, environmentID); bumpErr != nil {
					return bumpErr
				}
				bumped[environmentID] = true
			}
			buildSpec := desiredApplicationSpec(
				res,
				resourceSpec,
				hostname,
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
	})
	if err != nil {
		return deploymentTxError(ctx, err)
	}
	return nil
}

func inheritDesiredEnv(ctx context.Context, qtx *genDb.Queries, plan regionRedeploy) error {
	if plan.envSourceCluster == nil {
		return nil
	}
	service := plan.deploymentSpec.GetService()
	if service == nil {
		return errors.New("redeploy plan has no service spec")
	}
	_, err := qtx.LockResourceRegion(ctx, genDb.LockResourceRegionParams{
		ResourceID: plan.params.ResourceID,
		Region:     plan.params.Region,
	})
	if err != nil {
		return fmt.Errorf("failed to lock resource region: %w", err)
	}
	env, err := desiredEnv(ctx, qtx, plan.params.ResourceID, *plan.envSourceCluster)
	if err != nil {
		return fmt.Errorf("failed to read desired env: %w", err)
	}
	service.Env = env
	return nil
}

// resourceStatusToProto converts database resource status to proto enum
func resourceStatusToProto(status genDb.ResourceStatus) resourcev1.ResourceStatus {
	switch status {
	case genDb.ResourceStatusHealthy:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	case genDb.ResourceStatusDeploying:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	case genDb.ResourceStatusDegraded:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	case genDb.ResourceStatusUnavailable:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	case genDb.ResourceStatusSuspended:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_SUSPENDED
	default:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	}
}

// deploymentStatusToProto converts database deployment status to proto enum
func deploymentStatusToProto(status genDb.DeploymentStatus) deploymentv1.DeploymentPhase {
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

func validateScaleRequest(ctx context.Context, r *resourcev1.ScaleResourceRequest) error {
	if r.GetReplicas() == 0 && r.GetCpu() == "" && r.GetMemory() == "" {
		return connect.NewError(connect.CodeInvalidArgument, errScaleNothingRequested)
	}

	if cpu := r.GetCpu(); cpu != "" {
		if _, err := servicedefaults.ParseCPU(cpu); err != nil {
			slog.WarnContext(ctx, "invalid cpu format", "cpu", cpu, "error", err)
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	if memory := r.GetMemory(); memory != "" {
		if _, err := servicedefaults.ParseMemory(memory); err != nil {
			slog.WarnContext(ctx, "invalid memory format", "memory", memory, "error", err)
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	return nil
}

func selectRegionsToScale(requested string, resourceRegions []genDb.ResourceRegion) ([]string, error) {
	var regionsToScale []string
	if requested != "" {
		regionFound := false
		for _, rr := range resourceRegions {
			if rr.Region == requested {
				regionFound = true
				break
			}
		}
		if !regionFound {
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				fmt.Errorf("region '%s' not found for this resource", requested),
			)
		}
		regionsToScale = []string{requested}
	} else {
		for _, rr := range resourceRegions {
			regionsToScale = append(regionsToScale, rr.Region)
		}
	}

	if len(regionsToScale) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no regions found for resource"))
	}

	return regionsToScale, nil
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
		domainSource := domainv1.DomainType_DOMAIN_TYPE_USER_PROVIDED
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

		if d.SubdomainLabel != nil {
			domain.SubdomainLabel = d.SubdomainLabel
		}
		if d.PlatformDomainID != nil {
			s := d.PlatformDomainID.String()
			domain.PlatformDomainId = &s
		}

		protoDomains = append(protoDomains, domain)
	}
	return protoDomains
}

// dbResourceToProto converts a database Resource to the proto Resource
// to be returned to client. Note: caller is responsible for fetching domains and regions separately.
func dbResourceToProto(
	res genDb.Resource,
	domains []genDb.ResourceDomain,
	regions []genDb.ResourceRegion,
) *resourcev1.Resource {
	// convert db.ResourceType (string) to proto ResourceType (int32)
	var resourceType resourcev1.ResourceType
	switch res.Type {
	case genDb.ResourceTypeService:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_SERVICE
	case genDb.ResourceTypeDatabase:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_DATABASE
	case "function":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_FUNCTION
	case genDb.ResourceTypeCache:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_CACHE
	case genDb.ResourceTypeQueue:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_QUEUE
	case genDb.ResourceTypeBlob:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_BLOB
	default:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_SERVICE
	}

	resourceStatus := resourceStatusToProto(res.Status)

	protoRegions := make([]*resourcev1.RegionConfig, len(regions))
	for i, r := range regions {
		protoRegions[i] = &resourcev1.RegionConfig{
			Region:    r.Region,
			IsPrimary: r.IsPrimary,
		}
	}

	// reconstruct oneof spec from stored spec bytes
	var spec *resourcev1.ResourceSpec
	if len(res.Spec) > 0 {
		spec = reconstructResourceSpec(res.Type, res.Spec)
	}

	result := &resourcev1.Resource{
		Id:          res.ID.String(),
		WorkspaceId: res.WorkspaceID.String(),
		Name:        res.Name,
		Type:        resourceType,
		Spec:        spec,
		Domains:     resourceDomainToListProto(domains),
		Regions:     protoRegions,
		CreatedAt:   timeutil.ParsePostgresTimestamp(res.CreatedAt),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(res.UpdatedAt),
		Status:      resourceStatus,
		Description: &res.Description,
	}

	return result
}

// reconstructResourceSpec deserializes spec bytes and wraps in the appropriate oneof based on resource type
func reconstructResourceSpec(resourceType genDb.ResourceType, specBytes []byte) *resourcev1.ResourceSpec {
	if len(specBytes) == 0 {
		return nil
	}

	switch resourceType {
	case "service":
		serviceSpec := &resourcev1.ServiceSpec{}
		if err := protojson.Unmarshal(specBytes, serviceSpec); err != nil {
			slog.WarnContext(context.Background(), "failed to unmarshal service spec", "error", err)
			return nil
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{Service: serviceSpec},
		}
	case "database":
		databaseSpec := &resourcev1.DatabaseSpec{}
		if err := protojson.Unmarshal(specBytes, databaseSpec); err != nil {
			slog.WarnContext(context.Background(), "failed to unmarshal database spec", "error", err)
			return nil
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Database{Database: databaseSpec},
		}
	case "cache":
		cacheSpec := &resourcev1.CacheSpec{}
		if err := protojson.Unmarshal(specBytes, cacheSpec); err != nil {
			slog.WarnContext(context.Background(), "failed to unmarshal cache spec", "error", err)
			return nil
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Cache{Cache: cacheSpec},
		}
	case "queue":
		queueSpec := &resourcev1.QueueSpec{}
		if err := protojson.Unmarshal(specBytes, queueSpec); err != nil {
			slog.WarnContext(context.Background(), "failed to unmarshal queue spec", "error", err)
			return nil
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Queue{Queue: queueSpec},
		}
	case "blob":
		blobSpec := &resourcev1.BlobSpec{}
		if err := protojson.Unmarshal(specBytes, blobSpec); err != nil {
			slog.WarnContext(context.Background(), "failed to unmarshal blob spec", "error", err)
			return nil
		}
		return &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Blob{Blob: blobSpec},
		}
	default:
		slog.WarnContext(context.Background(), "unknown resource type", "type", resourceType)
		return nil
	}
}

var errDesiredSpec = errors.New("build desired spec")

type desiredSpecFunc func(deploymentID uuid.UUID) ([]byte, error)

func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(qtx *genDb.Queries) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		return fn(qtx)
	})
}

func primaryHostname(ctx context.Context, queries genDb.Querier, resourceID uuid.UUID) (string, error) {
	hostname, err := queries.GetPrimaryResourceDomain(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get primary domain: %w", err)
	}
	return hostname, nil
}

func desiredApplicationSpec(
	res genDb.Resource,
	resourceSpec *resourcev1.ResourceSpec,
	hostname string,
	deploymentSpec *deploymentv1.DeploymentSpec,
	region string,
	environmentID uuid.UUID,
	environmentName string,
	defaults servicedefaults.Defaults,
) desiredSpecFunc {
	return func(deploymentID uuid.UUID) ([]byte, error) {
		appSpec, err := buildApplicationSpec(
			res,
			resourceSpec,
			hostname,
			deploymentSpec,
			region,
			environmentID,
			environmentName,
			deploymentID,
			defaults,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build application spec: %w", err)
		}

		payload, err := json.Marshal(ApplicationPayload{
			DeploymentID: deploymentID.String(),
			ResourceID:   res.ID.String(),
			WorkspaceID:  res.WorkspaceID.String(),
			ResourceName: res.Name,
			ResourceType: string(res.Type),
			Region:       region,
			AppSpec:      appSpec,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal application payload: %w", err)
		}
		return payload, nil
	}
}

func deploymentTxError(ctx context.Context, err error) error {
	if invalidSpec, ok := errors.AsType[*invalidSpecError](err); ok {
		slog.WarnContext(ctx, "rejected an invalid deployment spec", "error", err)
		return connect.NewError(connect.CodeInvalidArgument, invalidSpec)
	}
	if errors.Is(err, errBuildImageDeleted) {
		slog.WarnContext(ctx, "rejected a deployment of a build whose image was deleted", "error", err)
		return connect.NewError(connect.CodeFailedPrecondition, errBuildImageDeleted)
	}
	if errors.Is(err, errDesiredSpec) {
		slog.ErrorContext(ctx, "failed to build desired application spec", "error", err)
		return connect.NewError(connect.CodeInternal, err)
	}
	if isPgConstraintViolation(err) {
		slog.WarnContext(ctx, "concurrent deployment rejected", "error", err)
		return connect.NewError(
			connect.CodeAborted,
			errors.New("another deployment for this resource and region is in progress"),
		)
	}
	slog.ErrorContext(ctx, "failed to create deployment", "error", err)
	return connect.NewError(connect.CodeInternal, ErrDB)
}

func finalizedDeploymentStatus(status genDb.DeploymentStatus) genDb.DeploymentStatus {
	switch status {
	case genDb.DeploymentStatusPending:
		return genDb.DeploymentStatusCanceled
	case genDb.DeploymentStatusDeploying:
		return genDb.DeploymentStatusCanceled
	case genDb.DeploymentStatusRunning:
		return genDb.DeploymentStatusSucceeded
	default:
		return status
	}
}

func createDeploymentWithCleanup(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.CreateDeploymentParams,
	buildSpec desiredSpecFunc,
) (uuid.UUID, error) {
	resourceRegion, err := qtx.LockResourceRegion(ctx, genDb.LockResourceRegionParams{
		ResourceID: params.ResourceID,
		Region:     params.Region,
	})
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to lock resource region: %w", err)
	}
	params.ResourceRegionID = resourceRegion.ID

	activeDeployment, err := qtx.GetActiveDeploymentForResourceAndRegion(
		ctx,
		genDb.GetActiveDeploymentForResourceAndRegionParams{
			ResourceID: params.ResourceID,
			Region:     params.Region,
		},
	)
	hadPreviousDeployment := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.UUID{}, fmt.Errorf("failed to get active deployment: %w", err)
	}

	if hadPreviousDeployment {
		newStatus := finalizedDeploymentStatus(activeDeployment.Status)
		slog.InfoContext(ctx, "finalizing previous deployment",
			"deploymentId", activeDeployment.ID,
			"oldStatus", activeDeployment.Status,
			"newStatus", newStatus)

		if updateErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       activeDeployment.ID,
			Status:   newStatus,
			IsActive: false,
		}); updateErr != nil {
			return uuid.UUID{}, fmt.Errorf("failed to finalize deployment %v: %w", activeDeployment.ID, updateErr)
		}
	}

	deploymentID, err := qtx.CreateDeployment(ctx, params)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("failed to create deployment: %w", err)
	}

	spec, err := buildSpec(deploymentID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: %w", errDesiredSpec, err)
	}

	if hadPreviousDeployment && activeDeployment.ClusterID != params.ClusterID {
		if removeErr := removePlacement(ctx, qtx, params.ResourceID, activeDeployment.ClusterID); removeErr != nil {
			return uuid.UUID{}, removeErr
		}
	}

	_, err = placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
		ResourceID:   params.ResourceID,
		ClusterID:    params.ClusterID,
		Region:       params.Region,
		DeploymentID: &deploymentID,
		DesiredSpec:  spec,
	})
	if err != nil {
		return uuid.UUID{}, err
	}

	slog.InfoContext(ctx, "deployment created",
		"deployment_id", deploymentID,
		"cluster_id", params.ClusterID,
		"resourceId", params.ResourceID,
		"region", params.Region,
		"hadPreviousDeployment", hadPreviousDeployment)

	return deploymentID, nil
}
