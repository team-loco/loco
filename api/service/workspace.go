package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/timeutil"
	workspacev1 "github.com/loco-team/loco/shared/proto/workspace/v1"
)

var (
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrWorkspaceNameNotUnique = errors.New("workspace name already exists in this organization")
	ErrInvalidWorkspaceName   = errors.New("workspace name must be DNS-safe: lowercase alphanumeric and hyphens only")
	ErrNotWorkspaceMember     = errors.New("user is not a member of this workspace")
	ErrNotWorkspaceAdmin      = errors.New("user is not an admin of this workspace")
	ErrWorkspaceHasApps       = errors.New("workspace has apps - must confirm deletion")
	ErrInvalidRole            = errors.New("invalid role - must be admin, deploy, or read")
)

var workspaceNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// WorkspaceServer implements the WorkspaceService gRPC server
type WorkspaceServer struct {
	db      *pgxpool.Pool
	queries *genDb.Queries
}

// NewWorkspaceServer creates a new WorkspaceServer instance
func NewWorkspaceServer(db *pgxpool.Pool, queries *genDb.Queries) *WorkspaceServer {
	return &WorkspaceServer{db: db, queries: queries}
}

// CreateWorkspace creates a new workspace
func (s *WorkspaceServer) CreateWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.CreateWorkspaceRequest],
) (*connect.Response[workspacev1.CreateWorkspaceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if !workspaceNamePattern.MatchString(r.Name) {
		slog.WarnContext(ctx, "invalid workspace name", "name", r.Name)
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidWorkspaceName)
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

	isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
		OrgID: r.OrgId,
		Name:  r.Name,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isUnique {
		slog.WarnContext(ctx, "workspace name already exists in org", "orgId", r.OrgId, "name", r.Name)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
	}

	description := pgtype.Text{String: r.GetDescription(), Valid: r.GetDescription() != ""}

	ws, err := s.queries.InsertWorkspace(ctx, genDb.InsertWorkspaceParams{
		OrgID:       r.OrgId,
		Name:        r.Name,
		Description: description,
		CreatedBy:   userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	_, err = s.queries.UpsertWorkspaceMember(ctx, genDb.UpsertWorkspaceMemberParams{
		WorkspaceID: ws.ID,
		UserID:      userID,
		Role:        genDb.WorkspaceRoleAdmin,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add workspace member", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&workspacev1.CreateWorkspaceResponse{
		Workspace: &workspacev1.Workspace{
			Id:          ws.ID,
			OrgId:       ws.OrgID,
			Name:        ws.Name,
			Description: ws.Description.String,
			CreatedBy:   ws.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
		},
	}), nil
}

// GetWorkspace retrieves a workspace by ID
func (s *WorkspaceServer) GetWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.GetWorkspaceRequest],
) (*connect.Response[workspacev1.GetWorkspaceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: r.Id,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.Id, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	ws, err := s.queries.GetWorkspaceByIDQuery(ctx, r.Id)
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.Id)
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return connect.NewResponse(&workspacev1.GetWorkspaceResponse{
		Workspace: &workspacev1.Workspace{
			Id:          ws.ID,
			OrgId:       ws.OrgID,
			Name:        ws.Name,
			Description: ws.Description.String,
			CreatedBy:   ws.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
		},
	}), nil
}

// GetUserWorkspaces retrieves all workspaces for the current user
func (s *WorkspaceServer) GetUserWorkspaces(
	ctx context.Context,
	req *connect.Request[workspacev1.GetUserWorkspacesRequest],
) (*connect.Response[workspacev1.GetUserWorkspacesResponse], error) {
	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceList, err := s.queries.ListWorkspacesForUser(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces for user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID,
			OrgId:       ws.OrgID,
			Name:        ws.Name,
			Description: ws.Description.String,
			CreatedBy:   ws.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
		})
	}

	return connect.NewResponse(&workspacev1.GetUserWorkspacesResponse{
		Workspaces: workspaces,
	}), nil
}

// ListWorkspaces lists all workspaces in an organization
func (s *WorkspaceServer) ListWorkspaces(
	ctx context.Context,
	req *connect.Request[workspacev1.ListWorkspacesRequest],
) (*connect.Response[workspacev1.ListWorkspacesResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
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

	workspaceList, err := s.queries.ListWorkspacesInOrg(ctx, r.OrgId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID,
			OrgId:       ws.OrgID,
			Name:        ws.Name,
			Description: ws.Description.String,
			CreatedBy:   ws.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
		})
	}

	return connect.NewResponse(&workspacev1.ListWorkspacesResponse{
		Workspaces: workspaces,
	}), nil
}

// UpdateWorkspace updates a workspace
func (s *WorkspaceServer) UpdateWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.UpdateWorkspaceRequest],
) (*connect.Response[workspacev1.UpdateWorkspaceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: r.Id,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.Id, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of workspace", "workspaceId", r.Id, "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceAdmin)
	}

	if r.GetName() != "" {
		if !workspaceNamePattern.MatchString(r.GetName()) {
			slog.WarnContext(ctx, "invalid workspace name", "name", r.GetName())
			return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidWorkspaceName)
		}

		orgID, err := s.queries.GetWorkspaceOrgID(ctx, r.Id)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get workspace org", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}

		isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
			OrgID: orgID,
			Name:  r.GetName(),
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}

		if !isUnique {
			slog.WarnContext(ctx, "workspace name already exists in org", "orgId", orgID, "name", r.GetName())
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
		}
	}

	name := pgtype.Text{String: r.GetName(), Valid: r.GetName() != ""}
	description := pgtype.Text{String: r.GetDescription(), Valid: r.GetDescription() != ""}

	ws, err := s.queries.UpdateWorkspace(ctx, genDb.UpdateWorkspaceParams{
		ID:          r.Id,
		Name:        name,
		Description: description,
	})
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.Id)
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return connect.NewResponse(&workspacev1.UpdateWorkspaceResponse{
		Workspace: &workspacev1.Workspace{
			Id:          ws.ID,
			OrgId:       ws.OrgID,
			Name:        ws.Name,
			Description: ws.Description.String,
			CreatedBy:   ws.CreatedBy,
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
		},
	}), nil
}

// DeleteWorkspace deletes a workspace
func (s *WorkspaceServer) DeleteWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.DeleteWorkspaceRequest],
) (*connect.Response[workspacev1.DeleteWorkspaceResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: r.Id,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.Id, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of workspace", "workspaceId", r.Id, "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceAdmin)
	}

	// TODO: Check if workspace has apps (when apps table exists)
	// For now, skip this check

	err = s.queries.RemoveWorkspace(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&workspacev1.DeleteWorkspaceResponse{
		Success: true,
	}), nil
}

// AddMember adds or updates a member in a workspace
func (s *WorkspaceServer) AddMember(
	ctx context.Context,
	req *connect.Request[workspacev1.AddMemberRequest],
) (*connect.Response[workspacev1.AddMemberResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	var wsRole genDb.WorkspaceRole
	switch r.Role {
	case "admin":
		wsRole = genDb.WorkspaceRoleAdmin
	case "deploy":
		wsRole = genDb.WorkspaceRoleDeploy
	case "read":
		wsRole = genDb.WorkspaceRoleRead
	default:
		slog.WarnContext(ctx, "invalid role", "role", r.Role)
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRole)
	}

	role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	if role != genDb.WorkspaceRoleAdmin {
		slog.WarnContext(ctx, "user is not an admin of workspace", "workspaceId", r.WorkspaceId, "userId", userID, "role", string(role))
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceAdmin)
	}

	member, err := s.queries.UpsertWorkspaceMember(ctx, genDb.UpsertWorkspaceMemberParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      r.UserId,
		Role:        wsRole,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add member", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&workspacev1.AddMemberResponse{
		Member: &workspacev1.WorkspaceMember{
			WorkspaceId: member.WorkspaceID,
			UserId:      member.UserID,
			Role:        string(member.Role),
			CreatedAt:   timeutil.ParsePostgresTimestamp(member.CreatedAt.Time),
		},
	}), nil
}

// RemoveMember removes a member from a workspace
func (s *WorkspaceServer) RemoveMember(
	ctx context.Context,
	req *connect.Request[workspacev1.RemoveMemberRequest],
) (*connect.Response[workspacev1.RemoveMemberResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	isSelfRemoval := userID == r.UserId

	if !isSelfRemoval {
		role, err := s.queries.GetWorkspaceMemberRole(ctx, genDb.GetWorkspaceMemberRoleParams{
			WorkspaceID: r.WorkspaceId,
			UserID:      userID,
		})
		if err != nil {
			slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
			return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
		}

		if role != genDb.WorkspaceRoleAdmin {
			slog.WarnContext(ctx, "user is not an admin of workspace", "workspaceId", r.WorkspaceId, "userId", userID, "role", string(role))
			return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceAdmin)
		}
	}

	deleteErr := s.queries.DeleteWorkspaceMember(ctx, genDb.DeleteWorkspaceMemberParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      r.UserId,
	})
	if deleteErr != nil {
		slog.ErrorContext(ctx, "failed to remove member", "error", deleteErr)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", deleteErr))
	}

	return connect.NewResponse(&workspacev1.RemoveMemberResponse{
		Success: true,
	}), nil
}

// ListMembers lists all members of a workspace
func (s *WorkspaceServer) ListMembers(
	ctx context.Context,
	req *connect.Request[workspacev1.ListMembersRequest],
) (*connect.Response[workspacev1.ListMembersResponse], error) {
	r := req.Msg

	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	isMember, err := s.queries.IsWorkspaceMember(ctx, genDb.IsWorkspaceMemberParams{
		WorkspaceID: r.WorkspaceId,
		UserID:      userID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace membership", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isMember {
		slog.WarnContext(ctx, "user is not a member of workspace", "workspaceId", r.WorkspaceId, "userId", userID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrNotWorkspaceMember)
	}

	memberList, err := s.queries.GetWorkspaceMembers(ctx, r.WorkspaceId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var members []*workspacev1.WorkspaceMember
	for _, member := range memberList {
		members = append(members, &workspacev1.WorkspaceMember{
			WorkspaceId: member.WorkspaceID,
			UserId:      member.UserID,
			Role:        string(member.Role),
			CreatedAt:   timeutil.ParsePostgresTimestamp(member.CreatedAt.Time),
		})
	}

	return connect.NewResponse(&workspacev1.ListMembersResponse{
		Members: members,
	}), nil
}
