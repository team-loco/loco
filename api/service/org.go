package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrOrgNotFound                   = errors.New("organization not found")
	ErrOrgNameNotUnique              = errors.New("organization name already exists")
	ErrOrgHasWorkspacesWithResources = errors.New("organization has workspaces with resources")
	ErrNotOrgMember                  = errors.New("user is not a member of this organization")
	ErrRemoveSelfFromOrg             = errors.New("you cannot remove yourself from the organization")
)

// OrgServer implements the OrgService gRPC server
type OrgServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	machine *tvm.VendingMachine
}

// NewOrgServer creates a new OrgServer instance
func NewOrgServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *OrgServer {
	return &OrgServer{
		db:      db,
		queries: queries,
		machine: machine,
		authz:   authz.New(db, queries),
	}
}

// CreateOrg creates a new organization
func (s *OrgServer) CreateOrg(
	ctx context.Context,
	req *orgv1.CreateOrgRequest,
) (*orgv1.CreateOrgResponse, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrImproperUsage)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateOrg, entity.ID.String()),
	); err != nil {
		slog.WarnContext(
			ctx,
			"unauthorized to create org",
			"entityId",
			entity.ID.String(),
			"entityType",
			entity.Type,
			"entityScopes",
			ctx.Value(contextkeys.EntityScopesKey),
		)
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	user, err := s.queries.GetUserByID(ctx, entity.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	orgName := req.GetName()
	if orgName == "" {
		orgName = fmt.Sprintf("%s's Organization", derefString(user.Name))
	}

	isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{Name: orgName})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if !isUnique {
		slog.WarnContext(ctx, "org name already exists", "name", orgName)
		return nil, rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	org, err := qtx.CreateOrg(ctx, genDb.CreateOrgParams{
		Name:      orgName,
		CreatedBy: entity.ID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	err = authz.ApplyRoles(ctx, qtx, entity.ID, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		slog.ErrorContext(
			ctx,
			"failed to update user roles for new organization",
			"error",
			err,
			"orgId",
			org.ID.String(),
			"userId",
			entity.ID.String(),
		)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.OrgCreated,
		OrgID:       new(org.ID),
		SubjectType: events.SubjectOrg,
		SubjectID:   new(org.ID),
		Data:        map[string]any{events.FieldName: org.Name},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record org creation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit organization creation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	return &orgv1.CreateOrgResponse{
		OrgId: org.ID.String(),
	}, nil
}

// GetOrg retrieves an organization by ID or name
func (s *OrgServer) GetOrg(
	ctx context.Context,
	req *orgv1.GetOrgRequest,
) (*orgv1.GetOrgResponse, error) {
	var org genDb.Organization
	var err error

	switch key := req.GetKey().(type) {
	case *orgv1.GetOrgRequest_OrgId:
		org, err = s.queries.GetOrgByID(ctx, uuid.MustParse(key.OrgId))
	case *orgv1.GetOrgRequest_OrgName:
		org, err = s.queries.GetOrgByName(ctx, key.OrgName)
	default:
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			"either org_id or org_name must be provided",
		)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrOrgNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to query org", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetOrg, org.ID.String()),
	); err != nil {
		// Return NotFound (not PermissionDenied) to prevent org-existence probing.
		slog.WarnContext(ctx, "unauthorized to get org", "orgId", org.ID.String())
		return nil, rpcerr.New(connect.CodeNotFound, ErrOrgNotFound)
	}

	return &orgv1.GetOrgResponse{
		Organization: &orgv1.Organization{
			Id:        org.ID.String(),
			Name:      org.Name,
			CreatedBy: org.CreatedBy.String(),
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt),
		},
	}, nil
}

// ListUserOrgs lists organizations for a user
func (s *OrgServer) ListUserOrgs(
	ctx context.Context,
	req *orgv1.ListUserOrgsRequest,
) (*orgv1.ListUserOrgsResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListUserOrgs, req.GetUserId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list user orgs", "userId", req.GetUserId())
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

	userID := uuid.MustParse(req.GetUserId())

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID:    userID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var orgResponses []*orgv1.Organization
	for _, org := range orgs {
		orgResponses = append(orgResponses, &orgv1.Organization{
			Id:        org.ID.String(),
			Name:      org.Name,
			CreatedBy: org.CreatedBy.String(),
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt),
		})
	}

	var nextPageToken string
	if len(orgs) == int(pageSize) {
		nextPageToken = encodeCursor(orgs[len(orgs)-1].ID.String())
	}

	return &orgv1.ListUserOrgsResponse{
		Orgs:          orgResponses,
		NextPageToken: nextPageToken,
	}, nil
}

// UpdateOrg updates an organization
func (s *OrgServer) UpdateOrg(
	ctx context.Context,
	req *orgv1.UpdateOrgRequest,
) (*orgv1.UpdateOrgResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateOrg, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update org", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	orgID := uuid.MustParse(req.GetOrgId())
	if req.GetName() != "" {
		isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{
			Name:      req.GetName(),
			ExcludeID: &orgID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
			return nil, rpcerr.New(connect.CodeInternal, ErrDB)
		}

		if !isUnique {
			slog.WarnContext(ctx, "org name already exists", "name", req.GetName())
			return nil, rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
	}

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if req.GetName() != "" {
			_, updateErr := qtx.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
				ID:   orgID,
				Name: req.GetName(),
			})
			if isPgConstraintViolation(updateErr) {
				return rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
			}
			if errors.Is(updateErr, pgx.ErrNoRows) {
				return rpcerr.New(connect.CodeNotFound, ErrOrgNotFound)
			}
			if updateErr != nil {
				return updateErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgUpdated,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to update org", err)
	}

	return &orgv1.UpdateOrgResponse{
		OrgId: req.GetOrgId(),
	}, nil
}

// DeleteOrg deletes an organization
func (s *OrgServer) DeleteOrg(
	ctx context.Context,
	req *orgv1.DeleteOrgRequest,
) (*orgv1.DeleteOrgResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteOrg, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete org", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	orgID := uuid.MustParse(req.GetOrgId())

	hasResources, err := s.queries.OrgHasWorkspacesWithResources(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check for resources in workspaces", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if hasResources {
		slog.WarnContext(ctx, "org has workspaces with resources", "orgId", req.GetOrgId())
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithResources)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if deleteErr := qtx.DeleteOrg(ctx, orgID); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgDeleted,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete org", err)
	}

	return &orgv1.DeleteOrgResponse{}, nil
}

func (s *OrgServer) ListOrgMembers(
	ctx context.Context,
	req *orgv1.ListOrgMembersRequest,
) (*orgv1.ListOrgMembersResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListOrgMembers, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list org members", "orgId", req.GetOrgId())
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

	rows, err := s.queries.ListOrgMembersWithDetails(ctx, genDb.ListOrgMembersWithDetailsParams{
		EntityID:  uuid.MustParse(req.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list org members", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	members := make([]*orgv1.OrgMember, len(rows))
	for i, row := range rows {
		user := &orgv1.User{
			Id:        row.ID.String(),
			Email:     row.Email,
			Name:      derefString(row.Name),
			AvatarUrl: derefString(row.AvatarUrl),
		}
		members[i] = &orgv1.OrgMember{User: user, Scopes: row.Scopes}
	}

	var nextPageToken string
	if len(rows) == int(pageSize) {
		lastID := rows[len(rows)-1].ID.String()
		nextPageToken = encodeCursor(lastID)
	}

	return &orgv1.ListOrgMembersResponse{
		Members:       members,
		NextPageToken: nextPageToken,
	}, nil
}

func (s *OrgServer) AddOrgMember(
	ctx context.Context,
	req *orgv1.AddOrgMemberRequest,
) (*orgv1.AddOrgMemberResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.AddOrgMember, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to add org member", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	orgID := uuid.MustParse(req.GetOrgId())
	userID := uuid.MustParse(req.GetUserId())
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if addErr := qtx.AddUserScope(ctx, genDb.AddUserScopeParams{
			UserID:     userID,
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   orgID,
			Scope:      genDb.ScopeMember,
		}); addErr != nil {
			return addErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgMemberAdded,
			OrgID:       new(orgID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if isPgForeignKeyViolation(err) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
	}
	if err != nil {
		return nil, txError(ctx, "failed to add org member", err)
	}

	return &orgv1.AddOrgMemberResponse{}, nil
}

func (s *OrgServer) RemoveOrgMember(
	ctx context.Context,
	req *orgv1.RemoveOrgMemberRequest,
) (*orgv1.RemoveOrgMemberResponse, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.RemoveOrgMember, req.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to remove org member", "orgId", req.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	orgID := uuid.MustParse(req.GetOrgId())
	userID := uuid.MustParse(req.GetUserId())
	if userID == entity.ID {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrRemoveSelfFromOrg)
	}

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if removeErr := qtx.RemoveOrgMember(ctx, genDb.RemoveOrgMemberParams{
			UserID: userID,
			OrgID:  orgID,
		}); removeErr != nil {
			return removeErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgMemberRemoved,
			OrgID:       new(orgID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to remove org member", err)
	}

	return &orgv1.RemoveOrgMemberResponse{}, nil
}

// ListOrgWorkspaces lists workspaces in an organization
func (s *OrgServer) ListOrgWorkspaces(
	ctx context.Context,
	req *orgv1.ListOrgWorkspacesRequest,
) (*orgv1.ListOrgWorkspacesResponse, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	// Check authorization
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaces, req.GetOrgId()),
	); err != nil {
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

	// Get workspaces for org
	workspaces, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:     uuid.MustParse(req.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	workspaceSummaries := make([]*orgv1.WorkspaceSummary, len(workspaces))
	for i, ws := range workspaces {
		workspaceSummaries[i] = &orgv1.WorkspaceSummary{
			Id:        ws.ID.String(),
			Name:      ws.Name,
			CreatedBy: ws.CreatedBy.String(),
			CreatedAt: timeutil.ParsePostgresTimestamp(ws.CreatedAt),
		}
	}

	var nextPageToken string
	if len(workspaces) == int(pageSize) {
		nextPageToken = encodeCursor(workspaces[len(workspaces)-1].ID.String())
	}

	return &orgv1.ListOrgWorkspacesResponse{
		Workspaces:    workspaceSummaries,
		NextPageToken: nextPageToken,
	}, nil
}
