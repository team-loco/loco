package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrWorkspaceNotFound     = rpcerr.NewCoded(connect.CodeNotFound, "workspace not found")
	ErrWorkspaceNameRequired = rpcerr.NewCoded(connect.CodeInvalidArgument, "workspace name must not be empty")
	ErrWorkspaceHasResources = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"workspace has resources, delete them first",
	)
)

// WorkspaceServer implements the WorkspaceService gRPC server
type WorkspaceServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
}

// NewWorkspaceServer creates a new WorkspaceServer instance
func NewWorkspaceServer(db *pgxpool.Pool, queries genDb.Querier) *WorkspaceServer {
	return &WorkspaceServer{db: db, queries: queries, authz: authz.New(db, queries)}
}

// CreateWorkspace creates a new workspace
func (s *WorkspaceServer) CreateWorkspace(
	ctx context.Context,
	req *workspacev1.CreateWorkspaceRequest,
) (*workspacev1.CreateWorkspaceResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.CreateWorkspace, orgID)); err != nil {
		return nil, err
	}
	if p.Entity.Type != genDb.EntityTypeUser {
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrImproperUsage)
	}
	var wsID uuid.UUID
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var createErr error
		wsID, createErr = createWorkspaceInTx(ctx, qtx, genDb.CreateWorkspaceParams{
			OrgID:       orgID,
			Name:        req.GetName(),
			Description: req.Description,
			CreatedBy:   p.Entity.ID,
		})
		return createErr
	})
	if err != nil {
		return nil, err
	}

	return &workspacev1.CreateWorkspaceResponse{
		WorkspaceId: wsID.String(),
	}, nil
}

func createWorkspaceInTx(
	ctx context.Context,
	qtx *genDb.Queries,
	params genDb.CreateWorkspaceParams,
) (uuid.UUID, error) {
	wsID, err := qtx.CreateWorkspace(ctx, params)
	if pgForeignKeyConstraint(err) == pgWorkspaceOrgReference {
		return uuid.Nil, ErrOrgNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert workspace: %w", err)
	}

	err = authz.ApplyRoles(ctx, qtx, params.CreatedBy, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: wsID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("grant roles on workspace %s: %w", wsID, err)
	}

	if _, err := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
		WorkspaceID:     wsID,
		Name:            environmentTypeProduction,
		EnvironmentType: environmentTypeProduction,
		CreatedBy:       params.CreatedBy,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("create production environment of workspace %s: %w", wsID, err)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.WorkspaceCreated,
		OrgID:       new(params.OrgID),
		WorkspaceID: new(wsID),
		SubjectType: events.SubjectWorkspace,
		SubjectID:   new(wsID),
		Data:        map[string]any{events.FieldName: params.Name},
	}); err != nil {
		return uuid.Nil, err
	}
	return wsID, nil
}

// GetWorkspace retrieves a workspace by ID
func (s *WorkspaceServer) GetWorkspace(
	ctx context.Context,
	req *workspacev1.GetWorkspaceRequest,
) (*workspacev1.GetWorkspaceResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.GetWorkspace, workspaceID); err != nil {
		return nil, err
	}
	ws, err := s.queries.GetWorkspaceByIDQuery(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}

	return &workspacev1.GetWorkspaceResponse{
		Workspace: workspaceToProto(ws),
	}, nil
}

// ListUserWorkspaces retrieves all workspaces for a user
func (s *WorkspaceServer) ListUserWorkspaces(
	ctx context.Context,
	req *workspacev1.ListUserWorkspacesRequest,
) (*workspacev1.ListUserWorkspacesResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if p.Entity.Type != genDb.EntityTypeUser {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrImproperUsage)
	}
	if err = check(ctx, s.authz, p, actions.New(actions.GetCurrentUserWorkspaces, p.Entity.ID)); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	workspaceList, err := s.queries.ListWorkspacesForUser(ctx, genDb.ListWorkspacesForUserParams{
		UserID:        p.Entity.ID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list user workspaces: %w", err)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, workspaceToProto(genDb.GetWorkspaceByIDQueryRow(ws)))
	}

	token := nextPageToken(
		workspaceList,
		pageSize,
		func(row genDb.ListWorkspacesForUserRow) (uuid.UUID, time.Time) { return row.ID, row.CreatedAt },
	)

	return &workspacev1.ListUserWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: token,
	}, nil
}

// ListOrgWorkspaces lists all workspaces in an organization
func (s *WorkspaceServer) ListOrgWorkspaces(
	ctx context.Context,
	req *workspacev1.ListOrgWorkspacesRequest,
) (*workspacev1.ListOrgWorkspacesResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.ListWorkspaces, orgID)); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	allWorkspaces, workspaceIDs := s.authz.ReadableWorkspaces(ctx, p.Scopes, orgID)

	workspaceList, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:         orgID,
		AllWorkspaces: allWorkspaces,
		WorkspaceIds:  workspaceIDs,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list organization workspaces: %w", err)
	}

	var workspaces []*workspacev1.Workspace
	for _, ws := range workspaceList {
		workspaces = append(workspaces, workspaceToProto(genDb.GetWorkspaceByIDQueryRow(ws)))
	}

	token := nextPageToken(
		workspaceList,
		pageSize,
		func(row genDb.ListWorkspacesInOrgRow) (uuid.UUID, time.Time) { return row.ID, row.CreatedAt },
	)

	return &workspacev1.ListOrgWorkspacesResponse{
		Workspaces:    workspaces,
		NextPageToken: token,
	}, nil
}

// UpdateWorkspace updates a workspace
func (s *WorkspaceServer) UpdateWorkspace(
	ctx context.Context,
	req *workspacev1.UpdateWorkspaceRequest,
) (*workspacev1.UpdateWorkspaceResponse, error) {
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.UpdateWorkspace, wsID); err != nil {
		return nil, err
	}

	message := req.ProtoReflect()
	fields := message.Descriptor().Fields()
	params := genDb.UpdateWorkspaceParams{ID: wsID}
	rename := message.Has(fields.ByName(updateNameField))
	if rename {
		if req.GetName() == "" {
			return nil, ErrWorkspaceNameRequired
		}
		params.Name = new(req.GetName())
	}
	if message.Has(fields.ByName(updateDescriptionField)) {
		params.Description = new(req.GetDescription())
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, updateErr := qtx.UpdateWorkspace(ctx, params)
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return ErrWorkspaceNotFound
		}
		if updateErr != nil {
			return fmt.Errorf("update workspace: %w", updateErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.WorkspaceUpdated,
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectWorkspace,
			SubjectID:   new(wsID),
		})
	})
	if err != nil {
		return nil, err
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
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.DeleteWorkspace, wsID); err != nil {
		return nil, err
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		deletedOrgID, orgErr := qtx.LockWorkspaceForDeletion(ctx, wsID)
		if errors.Is(orgErr, pgx.ErrNoRows) {
			return ErrWorkspaceNotFound
		}
		if orgErr != nil {
			return fmt.Errorf("lock workspace: %w", orgErr)
		}
		resources, listErr := qtx.ListWorkspaceResourcesForDeletion(ctx, wsID)
		if listErr != nil {
			return fmt.Errorf("list workspace resources: %w", listErr)
		}
		if len(resources) > 0 && !req.GetConfirmDeleteApps() {
			return ErrWorkspaceHasResources
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
			return fmt.Errorf("delete workspace: %w", removeErr)
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
		return nil, err
	}

	return &workspacev1.DeleteWorkspaceResponse{}, nil
}

// CreateMember adds a member to a workspace with the given scopes
func (s *WorkspaceServer) CreateMember(
	ctx context.Context,
	req *workspacev1.CreateMemberRequest,
) (*workspacev1.CreateMemberResponse, error) {
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.AddWorkspaceMember, wsID)); err != nil {
		return nil, err
	}

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
		if err = check(ctx, s.authz, p, requested); err != nil {
			return nil, err
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
			return ErrUserNotFound
		}
		if lockErr != nil {
			return fmt.Errorf("lock user: %w", lockErr)
		}
		orgID, orgErr := qtx.GetOrganizationIDByWorkspaceID(ctx, wsID)
		if errors.Is(orgErr, pgx.ErrNoRows) {
			return ErrWorkspaceNotFound
		}
		if orgErr != nil {
			return fmt.Errorf("get workspace organization: %w", orgErr)
		}
		isMember, memberErr := qtx.IsOrgMember(ctx, genDb.IsOrgMemberParams{UserID: userID, OrgID: orgID})
		if memberErr != nil {
			return fmt.Errorf("check organization membership: %w", memberErr)
		}
		if !isMember {
			return ErrNotOrgMember
		}
		applyErr := authz.ApplyRoles(ctx, qtx, userID, addScopes, nil)
		if pgForeignKeyConstraint(applyErr) == pgWorkspaceScopeReference {
			return ErrWorkspaceNotFound
		}
		if applyErr != nil {
			return applyErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.MemberAdded,
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
			Data:        map[string]any{events.FieldScopes: grantedScopes},
		})
	})
	if err != nil {
		return nil, err
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
	wsID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.RemoveWorkspaceMember, wsID); err != nil {
		return nil, err
	}

	userID, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidUserID).WithCause(err)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if removeErr := qtx.RemoveAllScopesForUserOnEntity(ctx, genDb.RemoveAllScopesForUserOnEntityParams{
			UserID:     userID,
			EntityType: genDb.EntityTypeWorkspace,
			EntityID:   wsID,
		}); removeErr != nil {
			return fmt.Errorf("remove workspace scopes: %w", removeErr)
		}
		resourceScopes := genDb.RemoveResourceScopesForUserInWorkspaceParams{UserID: userID, WorkspaceID: wsID}
		if removeErr := qtx.RemoveResourceScopesForUserInWorkspace(ctx, resourceScopes); removeErr != nil {
			return fmt.Errorf("remove resource scopes: %w", removeErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.MemberRemoved,
			WorkspaceID: new(wsID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, err
	}

	return &workspacev1.DeleteMemberResponse{}, nil
}

// ListWorkspaceMembers lists all members of a workspace with pagination
func (s *WorkspaceServer) ListWorkspaceMembers(
	ctx context.Context,
	req *workspacev1.ListWorkspaceMembersRequest,
) (*workspacev1.ListWorkspaceMembersResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListWorkspaceMembers, workspaceID); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	memberList, err := s.queries.ListWorkspaceMembersWithUserDetails(
		ctx,
		genDb.ListWorkspaceMembersWithUserDetailsParams{
			EntityID:      workspaceID,
			Limit:         pageSize,
			PageToken:     cursor.ID,
			PageCreatedAt: cursor.CreatedAt,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}

	var members []*workspacev1.WorkspaceMemberWithUser
	for _, member := range memberList {
		members = append(members, &workspacev1.WorkspaceMemberWithUser{
			WorkspaceId:   member.WorkspaceID.String(),
			UserId:        member.UserID.String(),
			Scopes:        member.Scopes,
			CreatedAt:     timestamppb.New(member.JoinedAt),
			UserName:      derefString(member.Name),
			UserEmail:     member.Email,
			UserAvatarUrl: derefString(member.AvatarUrl),
		})
	}

	token := nextPageToken(
		memberList,
		pageSize,
		func(m genDb.ListWorkspaceMembersWithUserDetailsRow) (uuid.UUID, time.Time) {
			return m.UserID, m.JoinedAt
		},
	)
	return &workspacev1.ListWorkspaceMembersResponse{
		Members:       members,
		NextPageToken: token,
	}, nil
}

// workspaceToProto takes the single-workspace row type; the list queries select the same columns
// and convert to it.
func workspaceToProto(ws genDb.GetWorkspaceByIDQueryRow) *workspacev1.Workspace {
	return &workspacev1.Workspace{
		Id:          ws.ID.String(),
		OrgId:       ws.OrgID.String(),
		OrgName:     ws.OrgName,
		Name:        ws.Name,
		Description: derefString(ws.Description),
		CreatedBy:   ws.CreatedBy.String(),
		CreatedAt:   timestamppb.New(ws.CreatedAt),
		UpdatedAt:   timestamppb.New(ws.UpdatedAt),
	}
}
