package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/commandbus"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	deploymentv1 "github.com/team-loco/loco/proto/loco/deployment/v1"
	domainv1 "github.com/team-loco/loco/proto/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/proto/loco/resource/v1"
	"github.com/team-loco/loco/proto/loco/resource/v1/resourcev1connect"
	"google.golang.org/protobuf/encoding/protojson"
	"k8s.io/apimachinery/pkg/api/resource"
)

var (
	ErrResourceNotFound      = errors.New("resource not found")
	ErrDomainNotFound        = errors.New("domain not found")
	ErrResourceNameNotUnique = errors.New("resource name already exists in this workspace")
	ErrSubdomainNotAvailable = errors.New("subdomain already in use")
	ErrClusterNotFound       = errors.New("cluster not found")
	ErrClusterNotHealthy     = errors.New("cluster is not healthy")
	ErrInvalidResourceType   = errors.New("invalid resource type")
	ErrInvalidCPU            = errors.New("invalid CPU format")
	ErrInvalidMemory         = errors.New("invalid memory format")
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
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
	cmdBus  commandbus.CommandBus
}

// NewResourceServer creates a new ResourceServer instance
func NewResourceServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine, cmdBus commandbus.CommandBus) *ResourceServer {
	return &ResourceServer{
		db:      db,
		queries: queries,
		machine: machine,
		cmdBus:  cmdBus,
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.CreateResource, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to create resource", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// validate that spec contains a service spec (for now, only services are supported)
	if r.GetSpec().GetService() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service resources are currently supported"))
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
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("platform domain not found"))
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
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("domain already in use"))
		}
	}

	// save only the oneof spec (e.g., ServiceSpec) to db, not the wrapper
	var (
		specJSON []byte
		err      error
	)
	switch specType := r.GetSpec().Spec.(type) {
	case *resourcev1.ResourceSpec_Service:
		specJSON, err = protojson.Marshal(specType.Service)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal service spec", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
		}
	case *resourcev1.ResourceSpec_Database:
		specJSON, err = protojson.Marshal(specType.Database)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal database spec", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
		}
	case *resourcev1.ResourceSpec_Cache:
		specJSON, err = protojson.Marshal(specType.Cache)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal cache spec", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
		}
	case *resourcev1.ResourceSpec_Queue:
		specJSON, err = protojson.Marshal(specType.Queue)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal queue spec", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
		}
	case *resourcev1.ResourceSpec_Blob:
		specJSON, err = protojson.Marshal(specType.Blob)
		if err != nil {
			slog.ErrorContext(ctx, "failed to marshal blob spec", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown resource spec type"))
	}

	resourceType, err := protoResourceTypeToDb(r.GetType())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	workspaceId := uuid.MustParse(r.GetWorkspaceId())

	params := genDb.CreateResourceParams{
		WorkspaceID: workspaceId,
		Name:        r.GetName(),
		Type:        resourceType,
		Status:      genDb.ResourceStatusUnavailable,
		Spec:        specJSON,
		SpecVersion: int32(1),
		Description: r.GetDescription(),
	}
	resourceID, err := s.queries.CreateResource(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create resource", "error", err)
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("a resource with this name already exists in this workspace"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create resource"))
	}

	// Create resource regions (first region is primary)
	for region, regionConfig := range serviceSpec.GetRegions() {
		isPrimary := regionConfig.GetPrimary()
		_, regionErr := s.queries.CreateResourceRegion(ctx, genDb.CreateResourceRegionParams{
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

		_, err = s.queries.CreateResourceDomain(ctx, domainParams)
		if err != nil {
			slog.ErrorContext(ctx, "failed to create resource domain", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
	}

	return connect.NewResponse(&resourcev1.CreateResourceResponse{ResourceId: resourceID.String()}), nil
}

// GetResource retrieves a resource by ID
func (s *ResourceServer) GetResource(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceRequest],
) (*connect.Response[resourcev1.GetResourceResponse], error) {
	r := req.Msg

	var resourceIdStr string
	switch key := r.GetKey().(type) {
	case *resourcev1.GetResourceRequest_ResourceId:
		resourceIdStr = key.ResourceId
	case *resourcev1.GetResourceRequest_NameKey:
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("name-based lookup not yet implemented"))
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("resource_id or name_key is required"))
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.GetResource, resourceIdStr)); err != nil {
		slog.WarnContext(ctx, "unauthorized to get resource", "resourceId", resourceIdStr)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceId, err := uuid.Parse(resourceIdStr)
	if err != nil {
		slog.ErrorContext(ctx, "invalid resource id format", "resourceId", resourceIdStr)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource id: %w", err))
	}

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "id", resourceIdStr)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.GetResourceResponse{
		Resource: dbResourceToProto(resource, resourceDomains, resourceRegions),
	}), nil
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.ListResources, r.GetWorkspaceId())); err != nil {
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

	wsId := uuid.MustParse(r.GetWorkspaceId())

	dbResources, err := s.queries.ListResourcesForWorkspace(ctx, genDb.ListResourcesForWorkspaceParams{
		WorkspaceID: wsId,
		Limit:       pageSize,
		PageToken:   pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resources", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var resources []*resourcev1.Resource
	for _, dbResource := range dbResources {
		resourceDomains, err := s.queries.ListResourceDomains(ctx, dbResource.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to list resource domains", "resourceId", dbResource.ID, "error", err)
			continue
		}
		resourceRegions, err := s.queries.ListResourceRegions(ctx, dbResource.ID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to list resource regions", "resourceId", dbResource.ID, "error", err)
			continue
		}
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.UpdateResource, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceId := uuid.MustParse(r.GetResourceId())

	updateParams := genDb.UpdateResourceParams{
		ID: resourceId,
	}

	if r.GetName() != "" {
		n := r.GetName()
		updateParams.Name = &n
	}

	_, err := s.queries.UpdateResource(ctx, updateParams)
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.DeleteResource, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceId := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	// Get active deployments to determine which clusters need delete commands
	activeDeployments, err := s.queries.ListActiveDeploymentsForResource(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	// Dispatch delete commands to all clusters with active deployments
	for _, deployment := range activeDeployments {
		cmdPayload := DeleteCommandPayload{
			DeploymentID: deployment.ID.String(),
			ResourceID:   resource.ID.String(),
		}

		payloadJSON, marshalErr := json.Marshal(cmdPayload)
		if marshalErr != nil {
			slog.ErrorContext(ctx, "failed to marshal delete command payload", "error", marshalErr)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to marshal command payload: %w", marshalErr))
		}

		// Dispatch delete command to the agent via CommandBus
		cmd := &commandbus.Command{
			ID:        uuid.NewString(),
			ClusterID: deployment.ClusterID.String(),
			Type:      commandbus.CommandTypeDelete,
			Payload:   payloadJSON,
			CreatedAt: time.Now(),
		}

		if sendErr := s.cmdBus.Send(ctx, cmd); sendErr != nil {
			slog.ErrorContext(ctx, "failed to dispatch delete command", "cluster_id", deployment.ClusterID, "error", sendErr)
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no agent connected for cluster: %w", sendErr))
		}

		slog.InfoContext(ctx, "delete command dispatched", "command_id", cmd.ID, "cluster_id", deployment.ClusterID, "resource_id", resource.ID)
	}

	err = s.queries.DeleteResource(ctx, resourceId)
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.GetResourceStatus, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to get resource status", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceId := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: resourceId,
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

	resourceDomains, err := s.queries.ListResourceDomains(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&resourcev1.GetResourceStatusResponse{
		Resource:          dbResourceToProto(resource, resourceDomains, resourceRegions),
		CurrentDeployment: deploymentStatus,
	}), nil
}

// ListRegions lists available regions for resource deployment
func (s *ResourceServer) ListRegions(
	ctx context.Context,
	req *connect.Request[resourcev1.ListRegionsRequest],
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.ScaleResource, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to scale resource", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if r.Replicas == nil && r.Cpu == nil && r.Memory == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one of replicas, cpu, or memory must be provided"))
	}

	if r.Cpu != nil && r.GetCpu() != "" {
		if _, err := resource.ParseQuantity(r.GetCpu()); err != nil {
			slog.WarnContext(ctx, "invalid cpu format", "cpu", r.GetCpu(), "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%w: %s", ErrInvalidCPU, r.GetCpu()))
		}
	}

	if r.Memory != nil && r.GetMemory() != "" {
		if _, err := resource.ParseQuantity(r.GetMemory()); err != nil {
			slog.WarnContext(ctx, "invalid memory format", "memory", r.GetMemory(), "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%w: %s", ErrInvalidMemory, r.GetMemory()))
		}
	}
	resourceId := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var regionsToScale []string
	if r.GetRegion() != "" {
		regionFound := false
		for _, rr := range resourceRegions {
			if rr.Region == r.GetRegion() {
				regionFound = true
				break
			}
		}
		if !regionFound {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("region '%s' not found for this resource", r.GetRegion()))
		}
		regionsToScale = []string{r.GetRegion()}
	} else {
		for _, rr := range resourceRegions {
			regionsToScale = append(regionsToScale, rr.Region)
		}
	}

	if len(regionsToScale) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no regions found for resource"))
	}

	deploymentList, err := s.queries.ListActiveDeploymentsForResource(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if len(deploymentList) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no active deployment found for resource"))
	}

	currentDeployment := deploymentList[0]
	if len(currentDeployment.Spec) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("previous deployment has no spec"))
	}

	deploymentSpec, deserializeErr := converter.DeserializeDeploymentSpec(currentDeployment.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, "failed to deserialize deployment spec", "error", deserializeErr)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", deserializeErr))
	}

	serviceDeploymentSpec := deploymentSpec.GetService()
	if serviceDeploymentSpec == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service resources are supported for scaling"))
	}

	// Check if any values are actually changing
	hasChanges := false

	if r.Cpu != nil {
		if serviceDeploymentSpec.Cpu == nil || r.GetCpu() != *serviceDeploymentSpec.Cpu {
			hasChanges = true
		}
	}

	if r.Memory != nil {
		if serviceDeploymentSpec.Memory == nil || r.GetMemory() != *serviceDeploymentSpec.Memory {
			hasChanges = true
		}
	}

	if r.Replicas != nil && r.GetReplicas() != currentDeployment.Replicas {
		hasChanges = true
	}

	if !hasChanges {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("scaling values must be different from current deployment"))
	}

	if r.Cpu != nil {
		serviceDeploymentSpec.Cpu = r.Cpu
	}

	if r.Memory != nil {
		serviceDeploymentSpec.Memory = r.Memory
	}

	specJson, err := protojson.Marshal(serviceDeploymentSpec)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal service deployment spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	replicas := currentDeployment.Replicas
	if r.Replicas != nil {
		replicas = r.GetReplicas()
	}

	// Get the region to scale (use current deployment's region)
	regionToScale := currentDeployment.Region

	// Get the environment tier (inherited from current deployment)
	deploymentEnv, err := s.queries.GetEnvironmentByID(ctx, currentDeployment.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment for deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	// Get the cluster for the region and tier
	cluster, err := s.queries.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
		Region: regionToScale,
		Tier:   deploymentEnv.EnvironmentType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster for region", "region", regionToScale, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("no active cluster available for region %s", regionToScale))
	}

	// Create deployment transactionally, finalizing previous deployments in the same region
	scaleDeploymentID, err := createDeploymentWithCleanup(ctx, s.db, s.queries, genDb.CreateDeploymentParams{
		ResourceID:    resourceId,
		ClusterID:     cluster.ID,
		Region:        regionToScale,
		Replicas:      replicas,
		Status:        genDb.DeploymentStatusPending,
		IsActive:      true,
		Message:       "Scheduled scaling event.",
		Spec:          specJson,
		SpecVersion:   int32(1),
		EnvironmentID: currentDeployment.EnvironmentID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	domain, err := s.queries.GetDomainByResourceId(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	resourceSpec, deserializeErr := converter.DeserializeResourceSpecByType(resource.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	scaleEnv, err := s.queries.GetEnvironmentByID(ctx, currentDeployment.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", currentDeployment.EnvironmentID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	updatedDeploymentSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: serviceDeploymentSpec,
		},
	}

	// Build the Application spec for the agent
	appSpec, err := buildApplicationSpec(
		resource,
		resourceSpec,
		domain.Domain,
		updatedDeploymentSpec,
		regionToScale,
		currentDeployment.EnvironmentID,
		scaleEnv.Name,
		scaleDeploymentID,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build application spec", "error", err, "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to build application spec: %w", err))
	}

	// Create command payload with all info the agent needs
	cmdPayload := DeployCommandPayload{
		DeploymentID: scaleDeploymentID.String(),
		ResourceID:   resource.ID.String(),
		WorkspaceID:  resource.WorkspaceID.String(),
		ResourceName: resource.Name,
		ResourceType: string(resource.Type),
		Region:       regionToScale,
		Hostname:     domain.Domain,
		AppSpec:      appSpec,
	}

	payloadJSON, err := json.Marshal(cmdPayload)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal command payload", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to marshal command payload: %w", err))
	}

	slog.InfoContext(ctx, "scale command payload created", "cluster_id", cluster.ID, "resource_id", resource.ID, "regions", regionsToScale)

	cmd := &commandbus.Command{
		ID:        uuid.NewString(),
		ClusterID: cluster.ID.String(),
		Type:      commandbus.CommandTypeDeploy,
		Payload:   payloadJSON,
		CreatedAt: time.Now(),
	}
	if err := s.cmdBus.Send(ctx, cmd); err != nil {
		slog.ErrorContext(ctx, "failed to dispatch scale command", "cluster_id", cluster.ID, "error", err)
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no agent connected for cluster: %w", err))
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.UpdateResourceEnv, r.GetResourceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update resource env", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if len(r.Env) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one environment variable must be provided"))
	}

	resourceId := uuid.MustParse(r.GetResourceId())

	resource, err := s.queries.GetResourceByID(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var regionsToUpdate []string
	if r.GetRegion() != "" {
		regionFound := false
		for _, rr := range resourceRegions {
			if rr.Region == r.GetRegion() {
				regionFound = true
				break
			}
		}
		if !regionFound {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("region '%s' not found for this resource", r.GetRegion()))
		}
		regionsToUpdate = []string{r.GetRegion()}
	} else {
		for _, rr := range resourceRegions {
			regionsToUpdate = append(regionsToUpdate, rr.Region)
		}
	}

	if len(regionsToUpdate) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no regions found for resource"))
	}

	deploymentList, err := s.queries.ListActiveDeploymentsForResource(ctx, resourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if len(deploymentList) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no active deployment found for resource"))
	}

	currentDeployment := deploymentList[0]
	if len(currentDeployment.Spec) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("previous deployment has no spec"))
	}

	deploymentSpec, deserializeErr := converter.DeserializeDeploymentSpec(currentDeployment.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, "failed to deserialize deployment spec", "error", deserializeErr)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", deserializeErr))
	}

	serviceDeploymentSpec := deploymentSpec.GetService()
	if serviceDeploymentSpec == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service resources are supported for env updates"))
	}

	serviceDeploymentSpec.Env = r.GetEnv()

	specJson, err := protojson.Marshal(serviceDeploymentSpec)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal service deployment spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	// Get the region to update (use current deployment's region)
	regionToUpdate := currentDeployment.Region

	// Get the environment tier (inherited from current deployment)
	deploymentEnv, err := s.queries.GetEnvironmentByID(ctx, currentDeployment.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment for deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	// Get the cluster for the region and tier
	cluster, err := s.queries.GetActiveClusterByRegionAndTier(ctx, genDb.GetActiveClusterByRegionAndTierParams{
		Region: regionToUpdate,
		Tier:   deploymentEnv.EnvironmentType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get active cluster for region", "region", regionToUpdate, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("no active cluster available for region %s", regionToUpdate))
	}

	// Create deployment transactionally, finalizing previous deployments in the same region
	deploymentId, err := createDeploymentWithCleanup(ctx, s.db, s.queries, genDb.CreateDeploymentParams{
		ResourceID:    resourceId,
		ClusterID:     cluster.ID,
		Region:        regionToUpdate,
		Replicas:      currentDeployment.Replicas,
		Status:        genDb.DeploymentStatusPending,
		IsActive:      true,
		Message:       "Scheduled environment update",
		Spec:          specJson,
		SpecVersion:   int32(1),
		EnvironmentID: currentDeployment.EnvironmentID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	domain, err := s.queries.GetDomainByResourceId(ctx, resourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	resourceSpec, deserializeErr := converter.DeserializeResourceSpecByType(resource.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	updateEnv, err := s.queries.GetEnvironmentByID(ctx, currentDeployment.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get environment", "error", err, "environmentId", currentDeployment.EnvironmentID)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	updatedDeploymentSpec := &deploymentv1.DeploymentSpec{
		Spec: &deploymentv1.DeploymentSpec_Service{
			Service: serviceDeploymentSpec,
		},
	}

	// Build the Application spec for the agent
	appSpec, err := buildApplicationSpec(
		resource,
		resourceSpec,
		domain.Domain,
		updatedDeploymentSpec,
		regionToUpdate,
		currentDeployment.EnvironmentID,
		updateEnv.Name,
		deploymentId,
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to build application spec", "error", err, "resourceId", resource.ID.String())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to build application spec: %w", err))
	}

	// Create command payload with all info the agent needs
	cmdPayload := DeployCommandPayload{
		DeploymentID: deploymentId.String(),
		ResourceID:   resource.ID.String(),
		WorkspaceID:  resource.WorkspaceID.String(),
		ResourceName: resource.Name,
		ResourceType: string(resource.Type),
		Region:       regionToUpdate,
		Hostname:     domain.Domain,
		AppSpec:      appSpec,
	}

	payloadJSON, err := json.Marshal(cmdPayload)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal command payload", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to marshal command payload: %w", err))
	}

	slog.InfoContext(ctx, "env update command payload created", "cluster_id", cluster.ID, "resource_id", resource.ID, "regions", regionsToUpdate, "deployment_id", deploymentId)

	cmd := &commandbus.Command{
		ID:        uuid.NewString(),
		ClusterID: cluster.ID.String(),
		Type:      commandbus.CommandTypeDeploy,
		Payload:   payloadJSON,
		CreatedAt: time.Now(),
	}
	if err := s.cmdBus.Send(ctx, cmd); err != nil {
		slog.ErrorContext(ctx, "failed to dispatch env update command", "cluster_id", cluster.ID, "error", err)
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("no agent connected for cluster: %w", err))
	}

	return connect.NewResponse(&resourcev1.UpdateResourceEnvResponse{}), nil
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

// resourceDomainToListProto converts a slice of ResourceDomain to proto ResourceDomain list
func resourceDomainToListProto(domains []genDb.ResourceDomain) []*domainv1.ResourceDomain {
	var protoDomains []*domainv1.ResourceDomain
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
func dbResourceToProto(resource genDb.Resource, domains []genDb.ResourceDomain, regions []genDb.ResourceRegion) *resourcev1.Resource {
	// convert db.ResourceType (string) to proto ResourceType (int32)
	var resourceType resourcev1.ResourceType
	switch resource.Type {
	case "service":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_SERVICE
	case "database":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_DATABASE
	case "function":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_FUNCTION
	case "cache":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_CACHE
	case "queue":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_QUEUE
	case "blob":
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_BLOB
	default:
		resourceType = resourcev1.ResourceType_RESOURCE_TYPE_SERVICE
	}

	resourceStatus := resourceStatusToProto(resource.Status)

	protoRegions := make([]*resourcev1.RegionConfig, len(regions))
	for i, r := range regions {
		protoRegions[i] = &resourcev1.RegionConfig{
			Region:    r.Region,
			IsPrimary: r.IsPrimary,
		}
	}

	// reconstruct oneof spec from stored spec bytes
	var spec *resourcev1.ResourceSpec
	if len(resource.Spec) > 0 {
		spec = reconstructResourceSpec(resource.Type, resource.Spec)
	}

	result := &resourcev1.Resource{
		Id:          resource.ID.String(),
		WorkspaceId: resource.WorkspaceID.String(),
		Name:        resource.Name,
		Type:        resourceType,
		Spec:        spec,
		Domains:     resourceDomainToListProto(domains),
		Regions:     protoRegions,
		CreatedAt:   timeutil.ParsePostgresTimestamp(resource.CreatedAt),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(resource.UpdatedAt),
		Status:      resourceStatus,
		Description: &resource.Description,
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

// createDeploymentWithCleanup creates a new deployment and finalizes previous active deployments in the same region
// within a transaction to ensure consistency.
func createDeploymentWithCleanup(
	ctx context.Context,
	pool *pgxpool.Pool,
	queries genDb.Querier,
	params genDb.CreateDeploymentParams,
) (uuid.UUID, error) {
	slog.InfoContext(ctx, "starting deployment creation with cleanup",
		"resourceId", params.ResourceID,
		"region", params.Region,
		"replicas", params.Replicas)

	// Get resource_region_id first (outside transaction since it's a read)
	resourceRegion, err := queries.GetResourceRegionByResourceAndRegion(ctx, genDb.GetResourceRegionByResourceAndRegionParams{
		ResourceID: params.ResourceID,
		Region:     params.Region,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource region",
			"resourceId", params.ResourceID,
			"region", params.Region,
			"error", err)
		return uuid.UUID{}, fmt.Errorf("failed to get resource region: %w", err)
	}
	params.ResourceRegionID = resourceRegion.ID

	tx, err := pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return uuid.UUID{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	// Find active deployment in the same region for this resource (should only be one)
	activeDeployment, err := qtx.GetActiveDeploymentForResourceAndRegion(ctx, genDb.GetActiveDeploymentForResourceAndRegionParams{
		ResourceID: params.ResourceID,
		Region:     params.Region,
	})

	hadPreviousDeployment := false
	// todo: rely on psql errors or something better. this is not good.
	if err != nil && err.Error() != "no rows in result set" {
		slog.ErrorContext(ctx, "failed to get active deployment",
			"resourceId", params.ResourceID,
			"region", params.Region,
			"error", err)
		return uuid.UUID{}, fmt.Errorf("failed to get active deployment: %w", err)
	}

	// Finalize the previous deployment if it exists
	if err == nil {
		hadPreviousDeployment = true

		// Determine new status based on current status
		var newStatus genDb.DeploymentStatus
		switch activeDeployment.Status {
		case genDb.DeploymentStatusPending, genDb.DeploymentStatusDeploying:
			newStatus = genDb.DeploymentStatusCanceled
		case genDb.DeploymentStatusRunning:
			newStatus = genDb.DeploymentStatusSucceeded
		default:
			// Keep existing status for terminal states
			newStatus = activeDeployment.Status
		}

		slog.InfoContext(ctx, "finalizing previous deployment",
			"deploymentId", activeDeployment.ID,
			"oldStatus", activeDeployment.Status,
			"newStatus", newStatus)

		if updateErr := qtx.UpdateDeploymentStatusAndActive(ctx, genDb.UpdateDeploymentStatusAndActiveParams{
			ID:       activeDeployment.ID,
			Status:   newStatus,
			IsActive: false,
		}); updateErr != nil {
			slog.ErrorContext(ctx, "failed to finalize deployment",
				"deploymentId", activeDeployment.ID,
				"error", updateErr)
			return uuid.UUID{}, fmt.Errorf("failed to finalize deployment %v: %w", activeDeployment.ID, updateErr)
		}
	}

	// Create the new deployment
	deploymentID, err := qtx.CreateDeployment(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment",
			"resourceId", params.ResourceID,
			"region", params.Region,
			"error", err)
		return uuid.UUID{}, fmt.Errorf("failed to create deployment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit transaction",
			"deploymentId", deploymentID,
			"error", err)
		return uuid.UUID{}, fmt.Errorf("failed to commit transaction: %w", err)
	}

	slog.InfoContext(ctx, "successfully created deployment with cleanup",
		"deployment_id", deploymentID,
		"resourceId", params.ResourceID,
		"region", params.Region,
		"hadPreviousDeployment", hadPreviousDeployment)

	return deploymentID, nil
}
