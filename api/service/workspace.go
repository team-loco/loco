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
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	workspacev1 "github.com/team-loco/loco/shared/proto/workspace/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrWorkspaceNameNotUnique = errors.New("workspace name already exists in this organization")
	ErrInvalidWorkspaceName   = errors.New("workspace name must be DNS-safe: lowercase alphanumeric and hyphens only")
	ErrNotWorkspaceMember     = errors.New("user is not a member of this workspace")
	ErrNotWorkspaceAdmin      = errors.New("user is not an admin of this workspace")
	ErrWorkspaceHasResources  = errors.New("workspace has resources - must confirm deletion")
	ErrInvalidRole            = errors.New("invalid role - must be admin, deploy, or read")
)

var workspaceNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// WorkspaceServer implements the WorkspaceService gRPC server
type WorkspaceServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
}

// NewWorkspaceServer creates a new WorkspaceServer instance
func NewWorkspaceServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *WorkspaceServer {
	return &WorkspaceServer{db: db, queries: queries, machine: machine}
}

// CreateWorkspace creates a new workspace
func (s *WorkspaceServer) CreateWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.CreateWorkspaceRequest],
) (*connect.Response[workspacev1.CreateWorkspaceResponse], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.CreateWorkspace, r.GetOrgId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to create workspace", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if !workspaceNamePattern.MatchString(r.GetName()) {
		slog.WarnContext(ctx, "invalid workspace name", "name", r.GetName())
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidWorkspaceName)
	}

	isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
		OrgID: r.GetOrgId(),
		Name:  r.GetName(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if !isUnique {
		slog.WarnContext(ctx, "workspace name already exists in org", "orgId", r.GetOrgId(), "name", r.GetName())
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
	}

	description := pgtype.Text{String: r.GetDescription(), Valid: r.GetDescription() != ""}

	wsID, err := s.queries.CreateWorkspace(ctx, genDb.CreateWorkspaceParams{
		OrgID:       r.GetOrgId(),
		Name:        r.GetName(),
		Description: description,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	entity := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	err = s.machine.UpdateRoles(ctx, entity.ID, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update user roles for new workspace", "error", err, "workspaceId", wsID, "userId", entity.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&workspacev1.CreateWorkspaceResponse{
		WorkspaceId: wsID,
	}), nil
}

// GetWorkspace retrieves a workspace by ID
func (s *WorkspaceServer) GetWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.GetWorkspaceRequest],
) (*connect.Response[workspacev1.Workspace], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.GetWorkspace, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to get workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	ws, err := s.queries.GetWorkspaceByIDQuery(ctx, r.GetWorkspaceId())
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return connect.NewResponse(&workspacev1.Workspace{
		Id:          ws.ID,
		OrgId:       ws.OrgID,
		Name:        ws.Name,
		Description: ws.Description.String,
		CreatedBy:   ws.CreatedBy,
		CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt.Time),
	}), nil
}

// ListUserWorkspaces retrieves all workspaces for a user
func (s *WorkspaceServer) ListUserWorkspaces(
	ctx context.Context,
	req *connect.Request[workspacev1.ListUserWorkspacesRequest],
) (*connect.Response[workspacev1.ListUserWorkspacesResponse], error) {
	r := req.Msg
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if entity.Type != genDb.EntityTypeUser {
		slog.ErrorContext(ctx, "entity is not a user", "entityType", entity.Type)
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrImproperUsage)
	}
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.GetCurrentUserWorkspaces, entity.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to get user workspaces", "userId", entity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = pgtype.Text{
			String: fmt.Sprintf("%d", cursorID),
			Valid:  true,
		}
	}

	workspaceList, err := s.queries.ListWorkspacesForUser(ctx, genDb.ListWorkspacesForUserParams{
		UserID:    entity.ID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
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

	var nextPageToken string
	if len(workspaceList) == int(pageSize) {
		nextPageToken = encodeCursor(workspaceList[len(workspaceList)-1].ID)
	}

	return connect.NewResponse(&workspacev1.ListUserWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: nextPageToken,
	}), nil
}

// ListOrgWorkspaces lists all workspaces in an organization
func (s *WorkspaceServer) ListOrgWorkspaces(
	ctx context.Context,
	req *connect.Request[workspacev1.ListOrgWorkspacesRequest],
) (*connect.Response[workspacev1.ListOrgWorkspacesResponse], error) {
	r := req.Msg
	slog.InfoContext(ctx, "list workspaces req for org", "orgId", r.GetOrgId())

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope),
		actions.New(actions.ListWorkspaces,
			r.GetOrgId(),
		),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspaces", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = pgtype.Text{
			String: fmt.Sprintf("%d", cursorID),
			Valid:  true,
		}
	}

	workspaceList, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:     r.GetOrgId(),
		Limit:     pageSize,
		PageToken: pageToken,
	})
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

	var nextPageToken string
	if len(workspaceList) == int(pageSize) {
		nextPageToken = encodeCursor(workspaceList[len(workspaceList)-1].ID)
	}

	return connect.NewResponse(&workspacev1.ListOrgWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: nextPageToken,
	}), nil
}

// UpdateWorkspace updates a workspace
func (s *WorkspaceServer) UpdateWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.UpdateWorkspaceRequest],
) (*connect.Response[workspacev1.UpdateWorkspaceResponse], error) {
	r := req.Msg

	entityScopes := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.UpdateWorkspace, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if r.GetName() != "" {
		if !workspaceNamePattern.MatchString(r.GetName()) {
			slog.WarnContext(ctx, "invalid workspace name", "name", r.GetName())
			return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidWorkspaceName)
		}

		orgID, err := s.queries.GetWorkspaceOrgID(ctx, r.GetWorkspaceId())
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

	_, err := s.queries.UpdateWorkspace(ctx, genDb.UpdateWorkspaceParams{
		ID:          r.GetWorkspaceId(),
		Name:        name,
		Description: description,
	})
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return connect.NewResponse(&workspacev1.UpdateWorkspaceResponse{
		WorkspaceId: r.GetWorkspaceId(),
	}), nil
}

// DeleteWorkspace deletes a workspace
func (s *WorkspaceServer) DeleteWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.DeleteWorkspaceRequest],
) (*connect.Response[emptypb.Empty], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.DeleteWorkspace, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	err := s.queries.RemoveWorkspace(ctx, r.GetWorkspaceId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// CreateMember adds a member to a workspace
func (s *WorkspaceServer) CreateMember(
	ctx context.Context,
	req *connect.Request[workspacev1.CreateMemberRequest],
) (*connect.Response[workspacev1.CreateMemberResponse], error) {
	r := req.Msg
	return connect.NewResponse(&workspacev1.CreateMemberResponse{
		WorkspaceId: r.GetWorkspaceId(),
		UserId:      r.GetUserId(),
	}), nil
}

// DeleteMember removes a member from a workspace
func (s *WorkspaceServer) DeleteMember(
	ctx context.Context,
	req *connect.Request[workspacev1.DeleteMemberRequest],
) (*connect.Response[emptypb.Empty], error) {
	// TODO: implement delete member
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

// ListWorkspaceMembers lists all members of a workspace with pagination
func (s *WorkspaceServer) ListWorkspaceMembers(
	ctx context.Context,
	req *connect.Request[workspacev1.ListWorkspaceMembersRequest],
) (*connect.Response[workspacev1.ListWorkspaceMembersResponse], error) {
	r := req.Msg

	entityScopes := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.ListWorkspaceMembers, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspace members", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = pgtype.Text{
			String: fmt.Sprintf("%d", cursorID),
			Valid:  true,
		}
	}

	memberList, err := s.queries.ListWorkspaceMembersWithUserDetails(ctx, genDb.ListWorkspaceMembersWithUserDetailsParams{
		WorkspaceID: r.GetWorkspaceId(),
		Limit:       pageSize,
		PageToken:   pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var members []*workspacev1.WorkspaceMemberWithUser
	for _, member := range memberList {
		members = append(members, &workspacev1.WorkspaceMemberWithUser{
			WorkspaceId:   member.WorkspaceID,
			UserId:        member.UserID,
			Role:          string(member.Role),
			CreatedAt:     timeutil.ParsePostgresTimestamp(member.CreatedAt.Time),
			UserName:      member.Name.String,
			UserEmail:     member.Email,
			UserAvatarUrl: member.AvatarUrl.String,
		})
	}

	var nextPageToken string
	if len(memberList) == int(pageSize) {
		nextPageToken = encodeCursor(memberList[len(memberList)-1].UserID)
	}

	return connect.NewResponse(&workspacev1.ListWorkspaceMembersResponse{
		Members:       members,
		NextPageToken: nextPageToken,
	}), nil
}
