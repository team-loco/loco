package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/loco-team/loco/api/contextkeys"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/pkg/converter"
	"github.com/loco-team/loco/api/pkg/klogmux"
	"github.com/loco-team/loco/api/pkg/kube"
	"github.com/loco-team/loco/api/timeutil"
	deploymentv1 "github.com/loco-team/loco/shared/proto/deployment/v1"
	domainv1 "github.com/loco-team/loco/shared/proto/domain/v1"
	resourcev1 "github.com/loco-team/loco/shared/proto/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ErrResourceNotFound      = errors.New("resource not found")
	ErrDomainNotFound        = errors.New("domain not found")
	ErrResourceNameNotUnique = errors.New("resource name already exists in this workspace")
	ErrSubdomainNotAvailable = errors.New("subdomain already in use")
	ErrClusterNotFound       = errors.New("cluster not found")
	ErrClusterNotHealthy     = errors.New("cluster is not healthy")
	ErrInvalidResourceType   = errors.New("invalid resource type")
)

// computeNamespace derives a Kubernetes namespace from resource ID
// format: app-{resourceID}
func computeNamespace(workspaceID, resourceID int64) string {
	return fmt.Sprintf("wks-%d-res-%d", workspaceID, resourceID)
}

type ResourceServer struct {
	db         *pgxpool.Pool
	queries    genDb.Querier
	kubeClient *kube.Client
}

// NewResourceServer creates a new ResourceServer instance
func NewResourceServer(db *pgxpool.Pool, queries genDb.Querier, kubeClient *kube.Client) *ResourceServer {
	// todo: move this out.
	return &ResourceServer{
		db:         db,
		queries:    queries,
		kubeClient: kubeClient,
	}
}

// CreateResource creates a new resource
func (s *ResourceServer) CreateResource(
	ctx context.Context,
	req *connect.Request[resourcev1.CreateResourceRequest],
) (*connect.Response[resourcev1.CreateResourceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	// todo: let tvm handle future validation.
	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != "admin" && role != "deploy" {
		slog.WarnContext(ctx, "user does not have permission to create resource", "workspaceId", r.WorkspaceId, "userId", userID, "role", role)
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin or have deploy role"))
	}

	if r.Spec == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec is required"))
	}

	// validate that spec contains a service spec (for now, only services are supported)
	if r.Spec.GetService() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("only service resources are currently supported"))
	}

	serviceSpec := r.Spec.GetService()
	if len(serviceSpec.Regions) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one region is required in spec"))
	}

	if r.Domain == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain is required"))
	}

	domainSource := genDb.DomainSourceUserProvided
	var fullDomain string
	var subdomainLabel pgtype.Text
	var platformDomainID pgtype.Int8

	if r.Domain.DomainSource == domainv1.DomainType_PLATFORM_PROVIDED {
		if r.Domain.Subdomain == nil || *r.Domain.Subdomain == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subdomain required for platform-provided domains"))
		}
		if r.Domain.PlatformDomainId == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("platform_domain_id required for platform-provided domains"))
		}

		domainSource = genDb.DomainSourcePlatformProvided
		platformDomainID = pgtype.Int8{Int64: *r.Domain.PlatformDomainId, Valid: true}

		platformDomain, err := s.queries.GetPlatformDomain(ctx, *r.Domain.PlatformDomainId)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get platform domain", "error", err)
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("platform domain not found"))
		}

		fullDomain = *r.Domain.Subdomain + "." + platformDomain.Domain
		subdomainLabel = pgtype.Text{String: *r.Domain.Subdomain, Valid: true}
	} else {
		if r.Domain.Domain == nil || *r.Domain.Domain == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("domain required for user-provided domains"))
		}
		fullDomain = *r.Domain.Domain
	}

	available, err := s.queries.CheckDomainAvailability(ctx, fullDomain)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check domain availability", "domain", fullDomain, "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !available {
		slog.WarnContext(ctx, "domain already in use", "domain", fullDomain)
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("domain already in use"))
	}

	if r.Spec == nil {
		slog.ErrorContext(ctx, "cannot create resource with nil spec")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("spec is required"))
	}

	// save only the oneof spec (e.g., ServiceSpec) to db, not the wrapper
	var specJSON []byte
	switch specType := r.Spec.Spec.(type) {
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

	params := genDb.CreateResourceParams{
		WorkspaceID: r.WorkspaceId,
		Name:        r.Name,
		Type:        genDb.ResourceType(strings.ToLower(r.Type.String())),
		Status:      genDb.ResourceStatusUnavailable,
		Spec:        specJSON,
		SpecVersion: 1,
		CreatedBy:   userID,
		Description: r.GetDescription(),
	}

	resourceID, err := s.queries.CreateResource(ctx, params)
	if err != nil {
		slog.ErrorContext(ctx, "failed to create resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	// Create resource regions (first region is primary)
	for region, regionConfig := range serviceSpec.Regions {
		isPrimary := regionConfig.Primary
		_, err := s.queries.CreateResourceRegion(ctx, genDb.CreateResourceRegionParams{
			ResourceID: resourceID,
			Region:     region,
			IsPrimary:  isPrimary,
			Status:     genDb.RegionIntentStatusDesired,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to create resource region", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
	}

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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&resourcev1.CreateResourceResponse{
		ResourceId: resourceID,
	}), nil
}

// GetResource retrieves a resource by ID
func (s *ResourceServer) GetResource(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceRequest],
) (*connect.Response[resourcev1.GetResourceResponse], error) {
	r := req.Msg

	// todo: role checks should actually be done first.
	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of resource's workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&resourcev1.GetResourceResponse{
		Resource: dbResourceToProto(resource, resourceDomains, resourceRegions),
	}), nil
}

// GetResourceByName retrieves a resource by workspace and name
func (s *ResourceServer) GetResourceByName(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceByNameRequest],
) (*connect.Response[resourcev1.GetResourceByNameResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
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

	resource, err := s.queries.GetResourceByNameAndWorkspace(ctx, genDb.GetResourceByNameAndWorkspaceParams{
		WorkspaceID: r.WorkspaceId,
		Name:        r.Name,
	})
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "workspaceId", r.WorkspaceId, "resource_name", r.Name)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&resourcev1.GetResourceByNameResponse{
		Resource: dbResourceToProto(resource, resourceDomains, resourceRegions),
	}), nil
}

// ListResources lists all resources in a workspace
func (s *ResourceServer) ListResources(
	ctx context.Context,
	req *connect.Request[resourcev1.ListResourcesRequest],
) (*connect.Response[resourcev1.ListResourcesResponse], error) {
	r := req.Msg

	slog.InfoContext(ctx, "received req to list resources", "workspaceId", r.WorkspaceId)
	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
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

	dbResources, err := s.queries.ListResourcesForWorkspace(ctx, r.WorkspaceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resources", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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

	return connect.NewResponse(&resourcev1.ListResourcesResponse{
		Resources: resources,
	}), nil
}

// UpdateResource updates a resource
func (s *ResourceServer) UpdateResource(
	ctx context.Context,
	req *connect.Request[resourcev1.UpdateResourceRequest],
) (*connect.Response[resourcev1.UpdateResourceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, r.ResourceId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
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
		slog.WarnContext(ctx, "user does not have permission to update resource", "workspaceId", fmt.Sprintf("%d", workspaceID), "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin or deploy role to update resource"))
	}

	updateParams := genDb.UpdateResourceParams{
		ID: r.ResourceId,
	}

	if r.GetName() != "" {
		updateParams.Name = pgtype.Text{String: r.GetName(), Valid: true}
	}

	resourceID, err := s.queries.UpdateResource(ctx, updateParams)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&resourcev1.UpdateResourceResponse{
		ResourceId: resourceID,
	}), nil
}

// DeleteResource deletes a resource
func (s *ResourceServer) DeleteResource(
	ctx context.Context,
	req *connect.Request[resourcev1.DeleteResourceRequest],
) (*connect.Response[resourcev1.DeleteResourceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID, err := s.queries.GetResourceWorkspaceID(ctx, r.ResourceId)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
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
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("must be workspace admin to delete resource"))
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if err := deleteLocoResource(ctx, s.kubeClient, resource.ID); err != nil {
		slog.ErrorContext(ctx, "failed to delete LocoResource during resource deletion", "error", err, "resource_id", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to cleanup LocoResource: %w", err))
	}

	err = s.queries.DeleteResource(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete resource", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&resourcev1.DeleteResourceResponse{
		ResourceId: resource.ID,
		Message:    "Resource deleted successfully",
	}), nil
}

// GetResourceStatus retrieves a resource and its current deployment status
func (s *ResourceServer) GetResourceStatus(
	ctx context.Context,
	req *connect.Request[resourcev1.GetResourceStatusRequest],
) (*connect.Response[resourcev1.GetResourceStatusResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of resource's workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	deploymentList, err := s.queries.ListDeploymentsForResource(ctx, genDb.ListDeploymentsForResourceParams{
		ResourceID: r.ResourceId,
		Limit:      1,
		Offset:     0,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var deploymentStatus *resourcev1.DeploymentStatus
	if len(deploymentList) > 0 {
		deployment := deploymentList[0]
		deploymentStatus = &resourcev1.DeploymentStatus{
			Id:       deployment.ID,
			Status:   deploymentStatusToProto(deployment.Status),
			Replicas: deployment.Replicas,
		}
		if deployment.Message.Valid {
			deploymentStatus.Message = &deployment.Message.String
		}
	}

	resourceDomains, err := s.queries.ListResourceDomains(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource domains", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	resourceRegions, err := s.queries.ListResourceRegions(ctx, resource.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	regionMap := make(map[string]*resourcev1.RegionInfo)
	for _, cluster := range clusters {
		if _, exists := regionMap[cluster.Region]; !exists {
			isDefault := cluster.IsDefault
			healthStatus := ""
			if cluster.HealthStatus.Valid {
				healthStatus = cluster.HealthStatus.String
			}
			regionMap[cluster.Region] = &resourcev1.RegionInfo{
				Region:       cluster.Region,
				IsDefault:    isDefault,
				HealthStatus: healthStatus,
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

// StreamLogs streams logs for a resource
func (s *ResourceServer) StreamLogs(
	ctx context.Context,
	req *connect.Request[resourcev1.StreamLogsRequest],
	stream *connect.ServerStream[resourcev1.LogEntry],
) error {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of resource's workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	slog.InfoContext(ctx, "fetching logs for resource", "resource_id", r.ResourceId)

	follow := false
	if r.Follow != nil {
		follow = *r.Follow
	}

	tailLines := int64(100)
	if r.Limit != nil {
		tailLines = int64(*r.Limit)
	}

	namespace := computeNamespace(resource.WorkspaceID, resource.ID)

	logStream := klogmux.NewBuilder(s.kubeClient.ClientSet).
		Namespace(namespace).
		Follow(follow).
		TailLines(tailLines).
		Timestamps(true).
		Build()

	if err := logStream.Start(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to start log stream", "error", err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to start log stream: %w", err))
	}
	defer logStream.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case entry := <-logStream.Entries():
			protoLog := &resourcev1.LogEntry{
				PodName:   entry.PodName,
				Namespace: entry.Namespace,
				Container: entry.Container,
				Timestamp: timestamppb.New(entry.Timestamp),
				Log:       entry.Message,
				Level:     "",
			}
			if entry.IsError {
				protoLog.Level = "error"
			}
			if err := stream.Send(protoLog); err != nil {
				slog.ErrorContext(ctx, "failed to send log to client", "error", err)
				return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to send logs: %w", err))
			}
		case err := <-logStream.Errors():
			if err != nil {
				slog.ErrorContext(ctx, "log stream error", "error", err)
				return connect.NewError(connect.CodeInternal, fmt.Errorf("log stream error: %w", err))
			}
		}
	}
}

// GetEvents retrieves Kubernetes events for a resource
func (s *ResourceServer) GetEvents(
	ctx context.Context,
	req *connect.Request[resourcev1.GetEventsRequest],
) (*connect.Response[resourcev1.GetEventsResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}

	_, err = s.queries.GetWorkspaceMember(ctx, genDb.GetWorkspaceMemberParams{
		WorkspaceID: resource.WorkspaceID,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of resource's workspace", "workspaceId", resource.WorkspaceID, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	namespace := computeNamespace(resource.WorkspaceID, resource.ID)

	slog.InfoContext(ctx, "fetching events for resource", "resource_id", r.ResourceId, "resource_namespace", namespace)

	eventList, err := s.kubeClient.ClientSet.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list events from kubernetes", "error", err, "namespace", namespace)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to fetch events: %w", err))
	}

	var protoEvents []*resourcev1.Event
	for _, k8sEvent := range eventList.Items {
		// filter events to those related to this resource's pods
		if k8sEvent.InvolvedObject.Kind != "Pod" {
			continue
		}

		protoEvent := &resourcev1.Event{
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

	slog.DebugContext(ctx, "fetched events for resource", "resource_id", r.ResourceId, "event_count", len(protoEvents))

	return connect.NewResponse(&resourcev1.GetEventsResponse{
		Events: protoEvents,
	}), nil
}

// ScaleResource scales a resource by creating a new deployment with updated resources
func (s *ResourceServer) ScaleResource(
	ctx context.Context,
	req *connect.Request[resourcev1.ScaleResourceRequest],
) (*connect.Response[resourcev1.ScaleResourceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
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

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	workspaceID := resource.WorkspaceID

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

	resourceRegions, err := s.queries.ListResourceRegions(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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

	deploymentList, err := s.queries.ListActiveDeploymentsForResource(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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
		replicas = *r.Replicas
	}

	err = s.queries.MarkPreviousDeploymentsNotActive(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark previous deployments not active", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deploymentId, err := s.queries.CreateDeployment(ctx, genDb.CreateDeploymentParams{
		ResourceID:  r.ResourceId,
		ClusterID:   1,
		Replicas:    replicas,
		Status:      genDb.DeploymentStatusPending,
		IsActive:    true,
		CreatedBy:   userID,
		Spec:        specJson,
		SpecVersion: 1,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	domain, err := s.queries.GetDomainByResourceId(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	resourceSpec, deserializeErr := converter.DeserializeResourceSpecByType(resource.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	err = createLocoResource(ctx, s.kubeClient, resource, resourceSpec, domain.Domain, nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update LocoResource", "error", err, "resource_id", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update LocoResource: %w", err))
	}
	slog.InfoContext(ctx, "updated LocoResource after scaling", "resource_id", resource.ID, "resource_name", resource.Name, "regions", regionsToScale)

	return connect.NewResponse(&resourcev1.ScaleResourceResponse{
		DeploymentId: deploymentId,
		Message:      "Scaling triggered.",
	}), nil
}

// UpdateResourceEnv updates environment variables for a resource
func (s *ResourceServer) UpdateResourceEnv(
	ctx context.Context,
	req *connect.Request[resourcev1.UpdateResourceEnvRequest],
) (*connect.Response[resourcev1.UpdateResourceEnvResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if len(r.Env) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one environment variable must be provided"))
	}

	resource, err := s.queries.GetResourceByID(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "resource not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	workspaceID := resource.WorkspaceID

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

	resourceRegions, err := s.queries.ListResourceRegions(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list resource regions", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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

	deploymentList, err := s.queries.ListActiveDeploymentsForResource(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list active deployments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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

	serviceDeploymentSpec.Env = r.Env

	specJson, err := protojson.Marshal(serviceDeploymentSpec)
	if err != nil {
		slog.ErrorContext(ctx, "failed to marshal service deployment spec", "error", err)
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid spec: %w", err))
	}

	err = s.queries.MarkPreviousDeploymentsNotActive(ctx, r.ResourceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to mark previous deployments not active", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	deploymentId, err := s.queries.CreateDeployment(ctx, genDb.CreateDeploymentParams{
		ResourceID:  r.ResourceId,
		ClusterID:   1,
		Replicas:    currentDeployment.Replicas,
		Status:      genDb.DeploymentStatusPending,
		IsActive:    true,
		CreatedBy:   userID,
		Spec:        specJson,
		SpecVersion: 1,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create deployment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	domain, err := s.queries.GetDomainByResourceId(ctx, r.ResourceId)
	if err != nil {
		slog.WarnContext(ctx, "domain not found", "resource_id", r.ResourceId)
		return nil, connect.NewError(connect.CodeNotFound, ErrDomainNotFound)
	}

	resourceSpec, deserializeErr := converter.DeserializeResourceSpecByType(resource.Spec, string(resource.Type))
	if deserializeErr != nil {
		slog.ErrorContext(ctx, deserializeErr.Error())
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid resource spec: %w", deserializeErr))
	}

	err = createLocoResource(ctx, s.kubeClient, resource, resourceSpec, domain.Domain, nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to update LocoResource", "error", err, "resource_id", resource.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update LocoResource: %w", err))
	}
	slog.InfoContext(ctx, "updated LocoResource after env update", "resource_id", resource.ID, "resource_name", resource.Name, "regions", regionsToUpdate)

	return connect.NewResponse(&resourcev1.UpdateResourceEnvResponse{
		DeploymentId: deploymentId,
		Message:      "Environment variables updated.",
	}), nil
}

// resourceStatusToProto converts database resource status to proto enum
func resourceStatusToProto(status genDb.ResourceStatus) resourcev1.ResourceStatus {
	switch status {
	case genDb.ResourceStatusHealthy:
		return resourcev1.ResourceStatus_HEALTHY
	case genDb.ResourceStatusDeploying:
		return resourcev1.ResourceStatus_DEPLOYING
	case genDb.ResourceStatusDegraded:
		return resourcev1.ResourceStatus_DEGRADED
	case genDb.ResourceStatusUnavailable:
		return resourcev1.ResourceStatus_UNAVAILABLE
	case genDb.ResourceStatusSuspended:
		return resourcev1.ResourceStatus_SUSPENDED
	default:
		return resourcev1.ResourceStatus_HEALTHY
	}
}

// deploymentStatusToProto converts database deployment status to proto enum
func deploymentStatusToProto(status genDb.DeploymentStatus) deploymentv1.DeploymentPhase {
	switch status {
	case genDb.DeploymentStatusPending:
		return deploymentv1.DeploymentPhase_PENDING
	case genDb.DeploymentStatusDeploying:
		return deploymentv1.DeploymentPhase_DEPLOYING
	case genDb.DeploymentStatusRunning:
		return deploymentv1.DeploymentPhase_RUNNING
	case genDb.DeploymentStatusSucceeded:
		return deploymentv1.DeploymentPhase_SUCCEEDED
	case genDb.DeploymentStatusFailed:
		return deploymentv1.DeploymentPhase_FAILED
	case genDb.DeploymentStatusCanceled:
		return deploymentv1.DeploymentPhase_CANCELED
	default:
		return deploymentv1.DeploymentPhase_UNSPECIFIED
	}
}

// resourceDomainToListProto converts a slice of ResourceDomain to proto ResourceDomain list
func resourceDomainToListProto(domains []genDb.ResourceDomain) []*domainv1.ResourceDomain {
	var protoDomains []*domainv1.ResourceDomain
	for _, d := range domains {
		domainSource := domainv1.DomainType_USER_PROVIDED
		if d.DomainSource == genDb.DomainSourcePlatformProvided {
			domainSource = domainv1.DomainType_PLATFORM_PROVIDED
		}

		domain := &domainv1.ResourceDomain{
			Id:           d.ID,
			ResourceId:   d.ResourceID,
			Domain:       d.Domain,
			DomainSource: domainSource,
			IsPrimary:    d.IsPrimary,
			CreatedAt:    timestamppb.New(d.CreatedAt.Time),
			UpdatedAt:    timestamppb.New(d.UpdatedAt.Time),
		}

		if d.SubdomainLabel.Valid {
			domain.SubdomainLabel = &d.SubdomainLabel.String
		}
		if d.PlatformDomainID.Valid {
			domain.PlatformDomainId = &d.PlatformDomainID.Int64
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
		resourceType = resourcev1.ResourceType_SERVICE
	case "database":
		resourceType = resourcev1.ResourceType_DATABASE
	case "function":
		resourceType = resourcev1.ResourceType_FUNCTION
	case "cache":
		resourceType = resourcev1.ResourceType_CACHE
	case "queue":
		resourceType = resourcev1.ResourceType_QUEUE
	case "blob":
		resourceType = resourcev1.ResourceType_BLOB
	default:
		resourceType = resourcev1.ResourceType_SERVICE
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
		Id:          resource.ID,
		WorkspaceId: resource.WorkspaceID,
		Name:        resource.Name,
		Type:        resourceType,
		Spec:        spec,
		Domains:     resourceDomainToListProto(domains),
		Regions:     protoRegions,
		CreatedBy:   resource.CreatedBy,
		CreatedAt:   timeutil.ParsePostgresTimestamp(resource.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(resource.UpdatedAt.Time),
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
