package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/contextkeys"
	"github.com/loco-team/loco/api/timeutil"
	orgv1 "github.com/loco-team/loco/shared/proto/org/v1"
)

var (
	ErrOrgNotFound              = errors.New("organization not found")
	ErrOrgNameNotUnique         = errors.New("organization name already exists")
	ErrOrgHasWorkspacesWithApps = errors.New("organization has workspaces with apps")
	ErrNotOrgMember             = errors.New("user is not a member of this organization")
	ErrNotOrgAdmin              = errors.New("user is not an admin of this organization")
)

// OrgServer implements the OrgService gRPC server
type OrgServer struct {
	db      *pgxpool.Pool
	queries *genDb.Queries
}

// NewOrgServer creates a new OrgServer instance
func NewOrgServer(db *pgxpool.Pool, queries *genDb.Queries) *OrgServer {
	return &OrgServer{db: db, queries: queries}
}

// CreateOrg creates a new organization
func (s *OrgServer) CreateOrg(
	ctx context.Context,
	req *connect.Request[orgv1.CreateOrgRequest],
) (*connect.Response[orgv1.CreateOrgResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	username, ok := ctx.Value(contextkeys.UserKey).(string)
	if !ok {
		slog.ErrorContext(ctx, "username not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	orgName := r.GetName()
	if orgName == "" {
		orgName = fmt.Sprintf("%s's Organization", username)
	}

	isUnique, err := s.queries.IsOrgNameUnique(ctx, orgName)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isUnique {
		slog.WarnContext(ctx, "org name already exists", "name", orgName)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
	}

	org, err := s.queries.CreateOrg(ctx, genDb.CreateOrgParams{
		Name:      orgName,
		CreatedBy: userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	err = s.queries.AddOrgMember(ctx, genDb.AddOrgMemberParams{
		OrganizationID: org.ID,
		UserID:         userID,
		Role:           genDb.OrganizationRoleAdmin,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add organization member", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&orgv1.CreateOrgResponse{
		Org: &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		},
	}), nil
}

// GetOrg retrieves an organization by ID
func (s *OrgServer) GetOrg(
	ctx context.Context,
	req *connect.Request[orgv1.GetOrgRequest],
) (*connect.Response[orgv1.GetOrgResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	isMember, err := s.queries.IsOrgMember(ctx, genDb.IsOrgMemberParams{
		OrganizationID: r.OrgId,
		UserID:         userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of org", "orgId", r.OrgId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgMember)
	}

	org, err := s.queries.GetOrgByID(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to query org", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}

	return connect.NewResponse(&orgv1.GetOrgResponse{
		Org: &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		},
	}), nil
}

// GetCurrentUserOrgs retrieves organizations for the current user
func (s *OrgServer) GetCurrentUserOrgs(
	ctx context.Context,
	req *connect.Request[orgv1.GetCurrentUserOrgsRequest],
) (*connect.Response[orgv1.GetCurrentUserOrgsResponse], error) {
	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	orgs, err := s.queries.ListUserOrganizations(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs for user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var orgResponses []*orgv1.Organization
	for _, org := range orgs {
		orgResponses = append(orgResponses, &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		})
	}

	return connect.NewResponse(&orgv1.GetCurrentUserOrgsResponse{
		Orgs: orgResponses,
	}), nil
}

// ListOrgs lists organizations for a specified user (admin endpoint)
// todo: implement loco management endpoints?
func (s *OrgServer) ListOrgs(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgsRequest],
) (*connect.Response[orgv1.ListOrgsResponse], error) {
	r := req.Msg

	limit := r.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := r.Offset

	totalCount, err := s.queries.CountOrgsForUser(ctx, r.UserId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count orgs for user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID: r.UserId,
		Offset: offset,
		Limit:  limit,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var orgResponses []*orgv1.Organization
	for _, org := range orgs {
		orgResponses = append(orgResponses, &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		})
	}

	return connect.NewResponse(&orgv1.ListOrgsResponse{
		Orgs:       orgResponses,
		TotalCount: totalCount,
	}), nil
}

// UpdateOrg updates an organization name
func (s *OrgServer) UpdateOrg(
	ctx context.Context,
	req *connect.Request[orgv1.UpdateOrgRequest],
) (*connect.Response[orgv1.UpdateOrgResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	role, err := s.queries.GetOrgMemberRole(ctx, genDb.GetOrgMemberRoleParams{
		OrganizationID: r.OrgId,
		UserID:         userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of org", "orgId", r.OrgId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgMember)
	}

	if role != genDb.OrganizationRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of org", "orgId", r.OrgId, "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgAdmin)
	}

	isUnique, err := s.queries.IsOrgNameUnique(ctx, r.NewName)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isUnique {
		existingOrg, err := s.queries.GetOrgByName(ctx, r.NewName)
		if err != nil || existingOrg.ID != r.OrgId {
			slog.WarnContext(ctx, "org name already exists", "name", r.NewName)
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
	}

	org, err := s.queries.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
		ID:   r.OrgId,
		Name: r.NewName,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update org", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}

	return connect.NewResponse(&orgv1.UpdateOrgResponse{
		Org: &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		},
	}), nil
}

// DeleteOrg deletes an organization
func (s *OrgServer) DeleteOrg(
	ctx context.Context,
	req *connect.Request[orgv1.DeleteOrgRequest],
) (*connect.Response[orgv1.DeleteOrgResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	role, err := s.queries.GetOrgMemberRole(ctx, genDb.GetOrgMemberRoleParams{
		OrganizationID: r.OrgId,
		UserID:         userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of org", "orgId", r.OrgId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgMember)
	}

	if role != genDb.OrganizationRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of org", "orgId", r.OrgId, "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgAdmin)
	}

	// TODO: Check if org has workspaces with apps or not.
	// var hasApps bool
	hasApps, err := s.queries.OrgHasWorkspacesWithApps(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check for apps in workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasApps {
		slog.WarnContext(ctx, "org has workspaces with apps", "orgId", r.OrgId)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithApps)
	}

	err = s.queries.DeleteOrg(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete org", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	org, err := s.queries.GetOrgByID(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get org", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&orgv1.DeleteOrgResponse{
		Org: &orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		},
		Message: "Organization deleted successfully",
	}), nil
}

// ListWorkspaces lists workspaces for an organization
func (s *OrgServer) ListWorkspaces(
	ctx context.Context,
	req *connect.Request[orgv1.ListWorkspacesRequest],
) (*connect.Response[orgv1.ListWorkspacesResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value(contextkeys.UserIDKey).(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	isMember, err := s.queries.IsOrgMember(ctx, genDb.IsOrgMemberParams{
		OrganizationID: r.OrgId,
		UserID:         userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of org", "orgId", r.OrgId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotOrgMember)
	}

	workspaces, err := s.queries.ListWorkspacesForOrg(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var workspaceResponses []*orgv1.WorkspaceSummary
	for _, ws := range workspaces {
		workspaceResponses = append(workspaceResponses, &orgv1.WorkspaceSummary{
			Id:        ws.ID,
			Name:      ws.Name,
			CreatedBy: ws.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
		})
	}

	return connect.NewResponse(&orgv1.ListWorkspacesResponse{
		Workspaces: workspaceResponses,
	}), nil
}

// IsUniqueOrgName checks if an org name is unique
func (s *OrgServer) IsUniqueOrgName(
	ctx context.Context,
	req *connect.Request[orgv1.IsUniqueOrgNameRequest],
) (*connect.Response[orgv1.IsUniqueOrgNameResponse], error) {
	r := req.Msg

	isUnique, err := s.queries.IsOrgNameUnique(ctx, r.GetName())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&orgv1.IsUniqueOrgNameResponse{
		IsUnique: isUnique,
	}), nil
}

// todo: need some sort of simple validate_access func.
