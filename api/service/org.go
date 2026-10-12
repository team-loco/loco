package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

var (
	ErrOrgNotFound                   = errors.New("organization not found")
	ErrOrgNameNotUnique              = errors.New("organization name already exists")
	ErrOrgHasWorkspacesWithResources = errors.New("organization has workspaces with resources")
	ErrNotOrgMember                  = errors.New("user is not a member of this organization")
	ErrNotOrgAdmin                   = errors.New("user is not an admin of this organization")
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
	r := req

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized.Error()).WithCause(ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrImproperUsage.Error()).WithCause(ErrImproperUsage)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
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
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}
	user, err := s.queries.GetUserByID(ctx, entity.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	orgName := r.GetName()
	if orgName == "" {
		orgName = fmt.Sprintf("%s's Organization", derefString(user.Name))
	}

	isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{Name: orgName})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	if !isUnique {
		slog.WarnContext(ctx, "org name already exists", "name", orgName)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique.Error()).
			WithCause(ErrOrgNameNotUnique)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	org, err := qtx.CreateOrg(ctx, genDb.CreateOrgParams{
		Name:      orgName,
		CreatedBy: entity.ID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique.Error()).
				WithCause(ErrOrgNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.OrgCreated,
		OrgID:       new(org.ID),
		SubjectType: events.SubjectOrg,
		SubjectID:   new(org.ID),
		Data:        map[string]any{events.FieldName: org.Name},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record org creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit organization creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
	r := req

	var org genDb.Organization
	var err error

	switch key := r.GetKey().(type) {
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
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound.Error()).WithCause(ErrOrgNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to query org", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetOrg, org.ID.String()),
	); err != nil {
		// Return NotFound (not PermissionDenied) to prevent org-existence probing.
		slog.WarnContext(ctx, "unauthorized to get org", "orgId", org.ID.String())
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound.Error()).WithCause(ErrOrgNotFound)
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
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListUserOrgs, r.GetUserId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list user orgs", "userId", r.GetUserId())
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken *string
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err).Error())
		}
		pageToken = &cursorID
	}

	userID := uuid.MustParse(r.GetUserId())

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID:    userID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateOrg, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}

	orgID := uuid.MustParse(r.GetOrgId())
	if r.GetName() != "" {
		isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{
			Name:      r.GetName(),
			ExcludeID: &orgID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
		}

		if !isUnique {
			slog.WarnContext(ctx, "org name already exists", "name", r.GetName())
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique.Error()).
				WithCause(ErrOrgNameNotUnique)
		}
	}

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if r.GetName() != "" {
			_, updateErr := qtx.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
				ID:   orgID,
				Name: r.GetName(),
			})
			if isPgConstraintViolation(updateErr) {
				return connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique.Error()).
					WithCause(ErrOrgNameNotUnique)
			}
			if errors.Is(updateErr, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, ErrOrgNotFound.Error()).WithCause(ErrOrgNotFound)
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
		OrgId: r.GetOrgId(),
	}, nil
}

// DeleteOrg deletes an organization
func (s *OrgServer) DeleteOrg(
	ctx context.Context,
	req *orgv1.DeleteOrgRequest,
) (*orgv1.DeleteOrgResponse, error) {
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteOrg, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}

	orgID := uuid.MustParse(r.GetOrgId())

	hasResources, err := s.queries.OrgHasWorkspacesWithResources(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check for resources in workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	if hasResources {
		slog.WarnContext(ctx, "org has workspaces with resources", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithResources.Error()).
			WithCause(ErrOrgHasWorkspacesWithResources)
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

// ListOrgUsers lists users in an organization
func (s *OrgServer) ListOrgUsers(
	ctx context.Context,
	req *orgv1.ListOrgUsersRequest,
) (*orgv1.ListOrgUsersResponse, error) {
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListOrgMembers, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list org users", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken *string
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err).Error())
		}
		pageToken = &cursorID
	}

	rows, err := s.queries.ListOrgUsersWithDetails(ctx, genDb.ListOrgUsersWithDetailsParams{
		EntityID:  uuid.MustParse(r.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list org users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	users := make([]*orgv1.User, len(rows))
	for i, row := range rows {
		users[i] = &orgv1.User{
			Id:        row.ID.String(),
			Email:     row.Email,
			Name:      derefString(row.Name),
			AvatarUrl: derefString(row.AvatarUrl),
		}
	}

	var nextPageToken string
	if len(rows) == int(pageSize) {
		lastID := rows[len(rows)-1].ID.String()
		nextPageToken = encodeCursor(lastID)
	}

	return &orgv1.ListOrgUsersResponse{
		Users:         users,
		NextPageToken: nextPageToken,
	}, nil
}

// ListOrgWorkspaces lists workspaces in an organization
func (s *OrgServer) ListOrgWorkspaces(
	ctx context.Context,
	req *orgv1.ListOrgWorkspacesRequest,
) (*orgv1.ListOrgWorkspacesResponse, error) {
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	// Check authorization
	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaces, r.GetOrgId()),
	); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken *string
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err).Error())
		}
		pageToken = &cursorID
	}

	// Get workspaces for org
	workspaces, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:     uuid.MustParse(r.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
