package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

var (
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrWorkspaceNameNotUnique = errors.New("workspace name already exists in this organization")
	ErrWorkspaceHasResources  = errors.New("workspace has resources, delete them first")
)

// WorkspaceServer implements the WorkspaceService gRPC server
type WorkspaceServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	machine *tvm.VendingMachine
}

// NewWorkspaceServer creates a new WorkspaceServer instance
func NewWorkspaceServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *WorkspaceServer {
	return &WorkspaceServer{db: db, queries: queries, machine: machine, authz: authz.New(db, queries)}
}

// CreateWorkspace creates a new workspace
func (s *WorkspaceServer) CreateWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.CreateWorkspaceRequest],
) (*connect.Response[workspacev1.CreateWorkspaceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateWorkspace, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create workspace", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	orgID := uuid.MustParse(r.GetOrgId())

	isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
		OrgID: orgID,
		Name:  r.GetName(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if !isUnique {
		slog.WarnContext(ctx, "workspace name already exists in org", "orgId", r.GetOrgId(), "name", r.GetName())
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrImproperUsage)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	wsID, err := qtx.CreateWorkspace(ctx, genDb.CreateWorkspaceParams{
		OrgID:       orgID,
		Name:        r.GetName(),
		Description: r.Description,
		CreatedBy:   entity.ID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	err = authz.ApplyRoles(ctx, qtx, entity.ID, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to update user roles for new workspace",
			"error",
			err,
			"workspaceId",
			wsID,
			"userId",
			entity.ID,
		)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if _, err := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
		WorkspaceID:     wsID,
		Name:            environmentTypeProduction,
		EnvironmentType: environmentTypeProduction,
		CreatedBy:       entity.ID,
	}); err != nil {
		slog.ErrorContext(
			ctx,
			"failed to create production environment for new workspace",
			"error",
			err,
			"workspaceId",
			wsID.String(),
		)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := events.RecordWith(ctx, qtx, events.Event{
		Type:        events.WorkspaceCreated,
		OrgID:       new(orgID),
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectWorkspace,
		SubjectID:   new(wsID),
		Data:        map[string]any{events.FieldName: r.GetName()},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record workspace creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit workspace creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&workspacev1.CreateWorkspaceResponse{
		WorkspaceId: wsID.String(),
	}), nil
}

// GetWorkspace retrieves a workspace by ID
func (s *WorkspaceServer) GetWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.GetWorkspaceRequest],
) (*connect.Response[workspacev1.GetWorkspaceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetWorkspace, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	ws, err := s.queries.GetWorkspaceByIDQuery(ctx, uuid.MustParse(r.GetWorkspaceId()))
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return connect.NewResponse(&workspacev1.GetWorkspaceResponse{
		Workspace: &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			Name:        ws.Name,
			Description: derefString(ws.Description),
			CreatedBy:   ws.CreatedBy.String(),
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt),
		},
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

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetCurrentUserWorkspaces, entity.ID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get user workspaces", "userId", entity.ID)
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

	workspaceList, err := s.queries.ListWorkspacesForUser(ctx, genDb.ListWorkspacesForUserParams{
		UserID:    entity.ID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces for user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			Name:        ws.Name,
			Description: derefString(ws.Description),
			CreatedBy:   ws.CreatedBy.String(),
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt),
		})
	}

	var nextPageToken string
	if len(workspaceList) == int(pageSize) {
		nextPageToken = encodeCursor(workspaceList[len(workspaceList)-1].ID.String())
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

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaces,
			r.GetOrgId(),
		),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspaces", "orgId", r.GetOrgId())
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

	orgID := uuid.MustParse(r.GetOrgId())

	workspaceList, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:     orgID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			Name:        ws.Name,
			Description: derefString(ws.Description),
			CreatedBy:   ws.CreatedBy.String(),
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt),
		})
	}

	var nextPageToken string
	if len(workspaceList) == int(pageSize) {
		nextPageToken = encodeCursor(workspaceList[len(workspaceList)-1].ID.String())
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

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateWorkspace, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if r.GetName() != "" {
		wsUUID := uuid.MustParse(r.GetWorkspaceId())

		orgID, err := s.queries.GetWorkspaceOrgID(ctx, wsUUID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get workspace org", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}

		isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
			OrgID:     orgID,
			Name:      r.GetName(),
			ExcludeID: &wsUUID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}

		if !isUnique {
			slog.WarnContext(ctx, "workspace name already exists in org", "orgId", orgID, "name", r.GetName())
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
		}
	}

	_, err := s.queries.UpdateWorkspace(ctx, genDb.UpdateWorkspaceParams{
		ID:          uuid.MustParse(r.GetWorkspaceId()),
		Name:        r.Name,
		Description: r.Description,
	})
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	events.Record(ctx, s.queries, events.Event{
		Type:        events.WorkspaceUpdated,
		WorkspaceID: new(uuid.MustParse(r.GetWorkspaceId())),
		SubjectType: events.SubjectWorkspace,
		SubjectID:   new(uuid.MustParse(r.GetWorkspaceId())),
	})
	return connect.NewResponse(&workspacev1.UpdateWorkspaceResponse{
		WorkspaceId: r.GetWorkspaceId(),
	}), nil
}

// DeleteWorkspace deletes a workspace
func (s *WorkspaceServer) DeleteWorkspace(
	ctx context.Context,
	req *connect.Request[workspacev1.DeleteWorkspaceRequest],
) (*connect.Response[workspacev1.DeleteWorkspaceResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteWorkspace, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete workspace", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	wsID := uuid.MustParse(r.GetWorkspaceId())

	if err := s.ensureWorkspaceHasNoResources(ctx, wsID); err != nil {
		return nil, err
	}

	deletedOrgID, orgErr := s.queries.GetOrganizationIDByWorkspaceID(ctx, wsID)
	if orgErr != nil {
		slog.WarnContext(ctx, "failed to look up workspace org for event", "error", orgErr)
	}
	if err := s.queries.RemoveWorkspace(ctx, wsID); err != nil {
		slog.ErrorContext(ctx, "failed to delete workspace", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	events.Record(ctx, s.queries, events.Event{
		Type:        events.WorkspaceDeleted,
		OrgID:       new(deletedOrgID),
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectWorkspace,
		SubjectID:   new(wsID),
	})
	return connect.NewResponse(&workspacev1.DeleteWorkspaceResponse{}), nil
}

func (s *WorkspaceServer) ensureWorkspaceHasNoResources(ctx context.Context, wsID uuid.UUID) error {
	hasResources, err := s.queries.WorkspaceHasResources(ctx, wsID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace for resources", "error", err)
		return connect.NewError(connect.CodeInternal, ErrDB)
	}
	if hasResources {
		slog.WarnContext(ctx, "workspace has resources", "workspaceId", wsID)
		return connect.NewError(connect.CodeFailedPrecondition, ErrWorkspaceHasResources)
	}
	return nil
}

// CreateMember adds a member to a workspace with the given scopes
func (s *WorkspaceServer) CreateMember(
	ctx context.Context,
	req *connect.Request[workspacev1.CreateMemberRequest],
) (*connect.Response[workspacev1.CreateMemberResponse], error) {
	r := req.Msg

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		entityScopes,
		actions.New(actions.AddWorkspaceMember, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to add workspace member", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	wsID := uuid.MustParse(r.GetWorkspaceId())
	validScopes := []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin}
	var addScopes []genDb.EntityScope
	for _, sc := range r.GetScopes() {
		if !slices.Contains(validScopes, sc) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid scope: %s", sc))
		}
		requested := genDb.EntityScope{
			EntityType: genDb.EntityTypeWorkspace,
			EntityID:   wsID,
			Scope:      sc,
		}
		if err := s.authz.Check(ctx, entityScopes, requested); err != nil {
			slog.WarnContext(
				ctx,
				"cannot grant a workspace scope the caller does not hold",
				"workspaceId",
				r.GetWorkspaceId(),
				"scope",
				sc,
			)
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		addScopes = append(addScopes, requested)
	}

	if err := s.authz.UpdateRoles(ctx, uuid.MustParse(r.GetUserId()), addScopes, []genDb.EntityScope{}); err != nil {
		if isPgForeignKeyViolation(err) {
			return nil, connect.NewError(connect.CodeNotFound, ErrUserNotFound)
		}
		slog.ErrorContext(ctx, "failed to add workspace member scopes", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	grantedScopes := make([]string, len(addScopes))
	for i, sc := range addScopes {
		grantedScopes[i] = sc.Scope
	}
	events.Record(ctx, s.queries, events.Event{
		Type:        events.MemberAdded,
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(uuid.MustParse(r.GetUserId())),
		Data:        map[string]any{"scopes": grantedScopes},
	})
	return connect.NewResponse(&workspacev1.CreateMemberResponse{
		WorkspaceId: r.GetWorkspaceId(),
		UserId:      r.GetUserId(),
	}), nil
}

// DeleteMember removes a member from a workspace by revoking all their workspace scopes
func (s *WorkspaceServer) DeleteMember(
	ctx context.Context,
	req *connect.Request[workspacev1.DeleteMemberRequest],
) (*connect.Response[workspacev1.DeleteMemberResponse], error) {
	r := req.Msg

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		entityScopes,
		actions.New(actions.RemoveWorkspaceMember, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to remove workspace member", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	wsID := uuid.MustParse(r.GetWorkspaceId())
	userID, err := uuid.Parse(r.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user id: %w", err))
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	if err := qtx.RemoveAllScopesForUserOnEntity(ctx, genDb.RemoveAllScopesForUserOnEntityParams{
		UserID:     userID,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   wsID,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to remove workspace member scopes", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := qtx.RemoveResourceScopesForUserInWorkspace(ctx, genDb.RemoveResourceScopesForUserInWorkspaceParams{
		UserID:      userID,
		WorkspaceID: wsID,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to remove member resource scopes", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := events.RecordWith(ctx, qtx, events.Event{
		Type:        events.MemberRemoved,
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(userID),
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record member removal", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit member removal", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&workspacev1.DeleteMemberResponse{}), nil
}

// ListWorkspaceMembers lists all members of a workspace with pagination
func (s *WorkspaceServer) ListWorkspaceMembers(
	ctx context.Context,
	req *connect.Request[workspacev1.ListWorkspaceMembersRequest],
) (*connect.Response[workspacev1.ListWorkspaceMembersResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaceMembers, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspace members", "workspaceId", r.GetWorkspaceId())
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

	memberList, err := s.queries.ListWorkspaceMembersWithUserDetails(
		ctx,
		genDb.ListWorkspaceMembersWithUserDetailsParams{
			EntityID:  uuid.MustParse(r.GetWorkspaceId()),
			Limit:     pageSize,
			PageToken: pageToken,
		},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	var members []*workspacev1.WorkspaceMemberWithUser
	for _, member := range memberList {
		members = append(members, &workspacev1.WorkspaceMemberWithUser{
			WorkspaceId:   member.WorkspaceID.String(),
			UserId:        member.UserID.String(),
			Scopes:        member.Scopes,
			CreatedAt:     timeutil.ParsePostgresTimestamp(member.JoinedAt),
			UserName:      derefString(member.Name),
			UserEmail:     member.Email,
			UserAvatarUrl: derefString(member.AvatarUrl),
		})
	}

	var nextPageToken string
	if len(memberList) == int(pageSize) {
		nextPageToken = encodeCursor(memberList[len(memberList)-1].UserID.String())
	}

	return connect.NewResponse(&workspacev1.ListWorkspaceMembersResponse{
		Members:       members,
		NextPageToken: nextPageToken,
	}), nil
}
