package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpcerr"
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
	req *workspacev1.CreateWorkspaceRequest,
) (*workspacev1.CreateWorkspaceResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateWorkspace, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create workspace", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	orgID := uuid.MustParse(req.GetOrgId())

	isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
		OrgID: orgID,
		Name:  req.GetName(),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if !isUnique {
		slog.WarnContext(ctx, "workspace name already exists in org", "orgId", req.GetOrgId(), "name", req.GetName())
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrImproperUsage)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	wsID, err := qtx.CreateWorkspace(ctx, genDb.CreateWorkspaceParams{
		OrgID:       orgID,
		Name:        req.GetName(),
		Description: req.Description,
		CreatedBy:   entity.ID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create workspace", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.WorkspaceCreated,
		OrgID:       new(orgID),
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectWorkspace,
		SubjectID:   new(wsID),
		Data:        map[string]any{events.FieldName: req.GetName()},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record workspace creation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit workspace creation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	return &workspacev1.CreateWorkspaceResponse{
		WorkspaceId: wsID.String(),
	}, nil
}

// GetWorkspace retrieves a workspace by ID
func (s *WorkspaceServer) GetWorkspace(
	ctx context.Context,
	req *workspacev1.GetWorkspaceRequest,
) (*workspacev1.GetWorkspaceResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetWorkspace, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get workspace", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	ws, err := s.queries.GetWorkspaceByIDQuery(ctx, uuid.MustParse(req.GetWorkspaceId()))
	if err != nil {
		slog.WarnContext(ctx, "workspace not found", "id", req.GetWorkspaceId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
	}

	return &workspacev1.GetWorkspaceResponse{
		Workspace: &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			OrgName:     ws.OrgName,
			Name:        ws.Name,
			Description: derefString(ws.Description),
			CreatedBy:   ws.CreatedBy.String(),
			CreatedAt:   timeutil.ParsePostgresTimestamp(ws.CreatedAt),
			UpdatedAt:   timeutil.ParsePostgresTimestamp(ws.UpdatedAt),
		},
	}, nil
}

// ListUserWorkspaces retrieves all workspaces for a user
func (s *WorkspaceServer) ListUserWorkspaces(
	ctx context.Context,
	req *workspacev1.ListUserWorkspacesRequest,
) (*workspacev1.ListUserWorkspacesResponse, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if entity.Type != genDb.EntityTypeUser {
		slog.ErrorContext(ctx, "entity is not a user", "entityType", entity.Type)
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrImproperUsage)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetCurrentUserWorkspaces, entity.ID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get user workspaces", "userId", entity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	pageSize := normalizePageSize(req.GetPageSize())

	var pageToken *string
	if req.GetPageToken() != "" {
		cursorID, err := decodeCursor(req.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
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
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			OrgName:     ws.OrgName,
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

	return &workspacev1.ListUserWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: nextPageToken,
	}, nil
}

// ListOrgWorkspaces lists all workspaces in an organization
func (s *WorkspaceServer) ListOrgWorkspaces(
	ctx context.Context,
	req *workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	slog.InfoContext(ctx, "list workspaces req for org", "orgId", req.GetOrgId())

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaces,
			req.GetOrgId(),
		),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspaces", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	pageSize := normalizePageSize(req.GetPageSize())

	var pageToken *string
	if req.GetPageToken() != "" {
		cursorID, err := decodeCursor(req.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
		}
		pageToken = &cursorID
	}

	orgID := uuid.MustParse(req.GetOrgId())
	allWorkspaces, workspaceIDs := s.authz.ReadableWorkspaces(ctx, scopes, orgID)

	workspaceList, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:         orgID,
		AllWorkspaces: allWorkspaces,
		WorkspaceIds:  workspaceIDs,
		Limit:         pageSize,
		PageToken:     pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, &workspacev1.Workspace{
			Id:          ws.ID.String(),
			OrgId:       ws.OrgID.String(),
			OrgName:     ws.OrgName,
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

	return &workspacev1.ListOrgWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: nextPageToken,
	}, nil
}

// UpdateWorkspace updates a workspace
func (s *WorkspaceServer) UpdateWorkspace(
	ctx context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateWorkspace, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update workspace", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	if req.GetName() != "" {
		wsUUID := uuid.MustParse(req.GetWorkspaceId())

		orgID, err := s.queries.GetWorkspaceOrgID(ctx, wsUUID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get workspace org", "error", err)
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}

		isUnique, err := s.queries.IsWorkspaceNameUniqueInOrg(ctx, genDb.IsWorkspaceNameUniqueInOrgParams{
			OrgID:     orgID,
			Name:      req.GetName(),
			ExcludeID: &wsUUID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check workspace name uniqueness", "error", err)
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}

		if !isUnique {
			slog.WarnContext(ctx, "workspace name already exists in org", "orgId", orgID, "name", req.GetName())
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrWorkspaceNameNotUnique)
		}
	}

	wsID := uuid.MustParse(req.GetWorkspaceId())
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, updateErr := qtx.UpdateWorkspace(ctx, genDb.UpdateWorkspaceParams{
			ID:          wsID,
			Name:        req.Name,
			Description: req.Description,
		})
		if errors.Is(updateErr, pgx.ErrNoRows) {
			slog.WarnContext(ctx, "workspace not found", "id", req.GetWorkspaceId())
			return rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		if updateErr != nil {
			return updateErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WorkspaceUpdated,
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectWorkspace,
			SubjectID:   new(wsID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to update workspace", err)
	}

	return &workspacev1.UpdateWorkspaceResponse{
		WorkspaceId: req.GetWorkspaceId(),
	}, nil
}

// DeleteWorkspace deletes a workspace
func (s *WorkspaceServer) DeleteWorkspace(
	ctx context.Context,
	req *workspacev1.DeleteWorkspaceRequest,
) (*workspacev1.DeleteWorkspaceResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteWorkspace, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete workspace", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	wsID := uuid.MustParse(req.GetWorkspaceId())

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		deletedOrgID, orgErr := qtx.LockWorkspaceForDeletion(ctx, wsID)
		if errors.Is(orgErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeNotFound, ErrWorkspaceNotFound)
		}
		if orgErr != nil {
			return orgErr
		}
		resources, listErr := qtx.ListWorkspaceResourcesForDeletion(ctx, wsID)
		if listErr != nil {
			return fmt.Errorf("list workspace resources: %w", listErr)
		}
		if len(resources) > 0 && !req.GetConfirmDeleteApps() {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrWorkspaceHasResources)
		}
		for _, resource := range resources {
			if removeErr := removeResourcePlacements(ctx, qtx, resource.ID); removeErr != nil {
				return removeErr
			}
			if deleteErr := qtx.DeleteResource(ctx, resource.ID); deleteErr != nil {
				return fmt.Errorf("delete workspace resource: %w", deleteErr)
			}
			event := events.Event{
				Type:        events.ResourceDeleted,
				OrgID:       new(deletedOrgID),
				WorkspaceID: new(wsID),
				SubjectType: events.SubjectResource,
				SubjectID:   new(resource.ID),
				Data:        map[string]any{events.FieldName: resource.Name},
			}
			if eventErr := events.Record(ctx, qtx, event); eventErr != nil {
				return eventErr
			}
		}
		if removeErr := qtx.RemoveWorkspace(ctx, wsID); removeErr != nil {
			return removeErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WorkspaceDeleted,
			OrgID:       new(deletedOrgID),
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectWorkspace,
			SubjectID:   new(wsID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete workspace", err)
	}

	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

// CreateMember adds a member to a workspace with the given scopes
func (s *WorkspaceServer) CreateMember(
	ctx context.Context,
	req *workspacev1.CreateMemberRequest,
) (*workspacev1.CreateMemberResponse, error) {
	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		entityScopes,
		actions.New(actions.AddWorkspaceMember, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to add workspace member", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	wsID := uuid.MustParse(req.GetWorkspaceId())
	validScopes := []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin}
	var addScopes []genDb.EntityScope
	for _, sc := range req.GetScopes() {
		if !slices.Contains(validScopes, sc) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid scope: %s", sc)
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
				req.GetWorkspaceId(),
				"scope",
				sc,
			)
			return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
		}
		addScopes = append(addScopes, requested)
	}

	userID, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidUserID).WithCause(err)
	}
	grantedScopes := make([]string, len(addScopes))
	for i, sc := range addScopes {
		grantedScopes[i] = sc.Scope
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, lockErr := qtx.LockUserForMembership(ctx, userID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if lockErr != nil {
			return lockErr
		}
		orgID, orgErr := qtx.GetOrganizationIDByWorkspaceID(ctx, wsID)
		if orgErr != nil {
			return orgErr
		}
		isMember, memberErr := qtx.IsOrgMember(ctx, genDb.IsOrgMemberParams{UserID: userID, OrgID: orgID})
		if memberErr != nil {
			return memberErr
		}
		if !isMember {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrNotOrgMember)
		}
		if applyErr := authz.ApplyRoles(ctx, qtx, userID, addScopes, nil); applyErr != nil {
			return applyErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.MemberAdded,
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
			Data:        map[string]any{"scopes": grantedScopes},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to add workspace member scopes", err)
	}

	return &workspacev1.CreateMemberResponse{
		WorkspaceId: req.GetWorkspaceId(),
		UserId:      req.GetUserId(),
	}, nil
}

// DeleteMember removes a member from a workspace by revoking all their workspace scopes
func (s *WorkspaceServer) DeleteMember(
	ctx context.Context,
	req *workspacev1.DeleteMemberRequest,
) (*workspacev1.DeleteMemberResponse, error) {
	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		entityScopes,
		actions.New(actions.RemoveWorkspaceMember, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to remove workspace member", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	wsID := uuid.MustParse(req.GetWorkspaceId())
	userID, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidUserID).WithCause(err)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	if err := qtx.RemoveAllScopesForUserOnEntity(ctx, genDb.RemoveAllScopesForUserOnEntityParams{
		UserID:     userID,
		EntityType: genDb.EntityTypeWorkspace,
		EntityID:   wsID,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to remove workspace member scopes", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := qtx.RemoveResourceScopesForUserInWorkspace(ctx, genDb.RemoveResourceScopesForUserInWorkspaceParams{
		UserID:      userID,
		WorkspaceID: wsID,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to remove member resource scopes", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.MemberRemoved,
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(userID),
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record member removal", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit member removal", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	return &workspacev1.DeleteMemberResponse{}, nil
}

// ListWorkspaceMembers lists all members of a workspace with pagination
func (s *WorkspaceServer) ListWorkspaceMembers(
	ctx context.Context,
	req *workspacev1.ListWorkspaceMembersRequest,
) (*workspacev1.ListWorkspaceMembersResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaceMembers, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list workspace members", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	pageSize := normalizePageSize(req.GetPageSize())

	var pageToken *string
	if req.GetPageToken() != "" {
		cursorID, err := decodeCursor(req.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(err)
		}
		pageToken = &cursorID
	}

	memberList, err := s.queries.ListWorkspaceMembersWithUserDetails(
		ctx,
		genDb.ListWorkspaceMembersWithUserDetailsParams{
			EntityID:  uuid.MustParse(req.GetWorkspaceId()),
			Limit:     pageSize,
			PageToken: pageToken,
		},
	)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list members", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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

	return &workspacev1.ListWorkspaceMembersResponse{
		Members:       members,
		NextPageToken: nextPageToken,
	}, nil
}
