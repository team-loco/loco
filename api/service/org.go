package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
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
	machine *tvm.VendingMachine
}

// NewOrgServer creates a new OrgServer instance
func NewOrgServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *OrgServer {
	return &OrgServer{db: db, queries: queries, machine: machine}
}

// CreateOrg creates a new organization
func (s *OrgServer) CreateOrg(
	ctx context.Context,
	req *connect.Request[orgv1.CreateOrgRequest],
) (*connect.Response[orgv1.CreateOrgResponse], error) {
	r := req.Msg

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrImproperUsage)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
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
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	user, err := s.queries.GetUserByID(ctx, entity.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	orgName := r.GetName()
	if orgName == "" {
		orgName = fmt.Sprintf("%s's Organization", derefString(user.Name))
	}

	isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{Name: orgName})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if !isUnique {
		slog.WarnContext(ctx, "org name already exists", "name", orgName)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	defer tx.Rollback(ctx)

	qtx := genDb.New(tx)

	org, err := qtx.CreateOrg(ctx, genDb.CreateOrgParams{
		Name:      orgName,
		CreatedBy: entity.ID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	err = tvm.ApplyRoles(ctx, qtx, entity.ID, []genDb.EntityScope{
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
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.OrgCreated,
		OrgID:       new(org.ID),
		SubjectType: events.SubjectOrg,
		SubjectID:   new(org.ID),
		Data:        map[string]any{events.FieldName: org.Name},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record org creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit organization creation", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	return connect.NewResponse(&orgv1.CreateOrgResponse{
		OrgId: org.ID.String(),
	}), nil
}

// GetOrg retrieves an organization by ID or name
func (s *OrgServer) GetOrg(
	ctx context.Context,
	req *connect.Request[orgv1.GetOrgRequest],
) (*connect.Response[orgv1.GetOrgResponse], error) {
	r := req.Msg

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
			errors.New("either org_id or org_name must be provided"),
		)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to query org", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.GetOrg, org.ID.String()),
	); err != nil {
		// Return NotFound (not PermissionDenied) to prevent org-existence probing.
		slog.WarnContext(ctx, "unauthorized to get org", "orgId", org.ID.String())
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}

	return connect.NewResponse(&orgv1.GetOrgResponse{
		Organization: &orgv1.Organization{
			Id:        org.ID.String(),
			Name:      org.Name,
			CreatedBy: org.CreatedBy.String(),
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt),
		},
	}), nil
}

// ListUserOrgs lists organizations for a user
func (s *OrgServer) ListUserOrgs(
	ctx context.Context,
	req *connect.Request[orgv1.ListUserOrgsRequest],
) (*connect.Response[orgv1.ListUserOrgsResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.ListUserOrgs, r.GetUserId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list user orgs", "userId", r.GetUserId())
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

	userID := uuid.MustParse(r.GetUserId())

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID:    userID,
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
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

	return connect.NewResponse(&orgv1.ListUserOrgsResponse{
		Orgs:          orgResponses,
		NextPageToken: nextPageToken,
	}), nil
}

// UpdateOrg updates an organization
func (s *OrgServer) UpdateOrg(
	ctx context.Context,
	req *connect.Request[orgv1.UpdateOrgRequest],
) (*connect.Response[orgv1.UpdateOrgResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.UpdateOrg, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to update org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	orgID := uuid.MustParse(r.GetOrgId())
	if r.GetName() != "" {
		isUnique, err := s.queries.IsOrgNameUnique(ctx, genDb.IsOrgNameUniqueParams{
			Name:      r.GetName(),
			ExcludeID: &orgID,
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}

		if !isUnique {
			slog.WarnContext(ctx, "org name already exists", "name", r.GetName())
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
	}

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if r.GetName() != "" {
			_, updateErr := qtx.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
				ID:   orgID,
				Name: r.GetName(),
			})
			if isPgConstraintViolation(updateErr) {
				return connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
			}
			if errors.Is(updateErr, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
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

	return connect.NewResponse(&orgv1.UpdateOrgResponse{
		OrgId: r.GetOrgId(),
	}), nil
}

// DeleteOrg deletes an organization
func (s *OrgServer) DeleteOrg(
	ctx context.Context,
	req *connect.Request[orgv1.DeleteOrgRequest],
) (*connect.Response[orgv1.DeleteOrgResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.DeleteOrg, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	orgID := uuid.MustParse(r.GetOrgId())

	hasResources, err := s.queries.OrgHasWorkspacesWithResources(ctx, orgID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check for resources in workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
	}

	if hasResources {
		slog.WarnContext(ctx, "org has workspaces with resources", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithResources)
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

	return connect.NewResponse(&orgv1.DeleteOrgResponse{}), nil
}

// ListOrgUsers lists users in an organization
func (s *OrgServer) ListOrgUsers(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgUsersRequest],
) (*connect.Response[orgv1.ListOrgUsersResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.ListOrgMembers, r.GetOrgId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list org users", "orgId", r.GetOrgId())
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

	rows, err := s.queries.ListOrgUsersWithDetails(ctx, genDb.ListOrgUsersWithDetailsParams{
		EntityID:  uuid.MustParse(r.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list org users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
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

	return connect.NewResponse(&orgv1.ListOrgUsersResponse{
		Users:         users,
		NextPageToken: nextPageToken,
	}), nil
}

// ListOrgWorkspaces lists workspaces in an organization
func (s *OrgServer) ListOrgWorkspaces(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgWorkspacesRequest],
) (*connect.Response[orgv1.ListOrgWorkspacesResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	// Check authorization
	if err := s.machine.VerifyWithGivenEntityScopes(
		ctx,
		scopes,
		actions.New(actions.ListWorkspaces, r.GetOrgId()),
	); err != nil {
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

	// Get workspaces for org
	workspaces, err := s.queries.ListWorkspacesInOrg(ctx, genDb.ListWorkspacesInOrgParams{
		OrgID:     uuid.MustParse(r.GetOrgId()),
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
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

	return connect.NewResponse(&orgv1.ListOrgWorkspacesResponse{
		Workspaces:    workspaceSummaries,
		NextPageToken: nextPageToken,
	}), nil
}
