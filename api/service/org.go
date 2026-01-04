package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	orgv1 "github.com/team-loco/loco/shared/proto/org/v1"
	"google.golang.org/protobuf/types/known/emptypb"
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
) (*connect.Response[orgv1.Organization], error) {
	r := req.Msg

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context", "entityType", reflect.TypeOf(ctx.Value(contextkeys.EntityKey)))
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	// make sure that requester is a user and has permission to create orgs (user:write on oneself)
	if entity.Type != genDb.EntityTypeUser {
		slog.WarnContext(ctx, "only users can create organizations", "entityId", entity.ID, "entityType", entity.Type)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrImproperUsage)
	}
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.CreateOrg, entity.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to create org", "entityId", entity.ID, "entityType", entity.Type, "entityScopes", ctx.Value(contextkeys.EntityScopesKey))
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	user, err := s.queries.GetUserByID(ctx, entity.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to get user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	orgName := r.GetName()
	if orgName == "" {
		orgName = fmt.Sprintf("%s's Organization", user.Name.String)
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
		CreatedBy: entity.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	err = s.queries.AddOrgMember(ctx, genDb.AddOrgMemberParams{
		OrganizationID: org.ID,
		UserID:         entity.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to add organization member", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&orgv1.Organization{
		Id:        org.ID,
		Name:      org.Name,
		CreatedBy: org.CreatedBy,
		CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
		UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
	}), nil
}

// GetOrg retrieves an organization by ID or name
func (s *OrgServer) GetOrg(
	ctx context.Context,
	req *connect.Request[orgv1.GetOrgRequest],
) (*connect.Response[orgv1.Organization], error) {
	r := req.Msg

	orgID := r.GetOrgId()
	orgName := r.GetOrgName()

	if orgID == 0 && orgName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("either org_id or org_name must be provided"))
	}

	var org genDb.Organization
	var err error

	if orgID != 0 {
		org, err = s.queries.GetOrgByID(ctx, orgID)
	} else {
		org, err = s.queries.GetOrgByName(ctx, orgName)
	}

	if err != nil {
		slog.ErrorContext(ctx, "failed to query org", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.GetOrg, org.ID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to get org", "orgId", org.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&orgv1.Organization{
		Id:        org.ID,
		Name:      org.Name,
		CreatedBy: org.CreatedBy,
		CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
		UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
	}), nil
}

// ListUserOrgs lists organizations for a user
func (s *OrgServer) ListUserOrgs(
	ctx context.Context,
	req *connect.Request[orgv1.ListUserOrgsRequest],
) (*connect.Response[orgv1.ListUserOrgsResponse], error) {
	r := req.Msg

	entityScopes := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.ListUserOrgs, r.GetUserId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to list user orgs", "userId", r.GetUserId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	limit := r.GetLimit()
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := r.GetOffset()

	totalCount, err := s.queries.CountOrgsForUser(ctx, r.GetUserId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to count orgs for user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID: r.GetUserId(),
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

	return connect.NewResponse(&orgv1.ListUserOrgsResponse{
		Orgs:       orgResponses,
		TotalCount: totalCount,
	}), nil
}

// UpdateOrg updates an organization
func (s *OrgServer) UpdateOrg(
	ctx context.Context,
	req *connect.Request[orgv1.UpdateOrgRequest],
) (*connect.Response[orgv1.Organization], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.UpdateOrg, r.GetOrgId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	if r.GetName() != "" {
		isUnique, err := s.queries.IsOrgNameUnique(ctx, r.GetName())
		if err != nil {
			slog.ErrorContext(ctx, "failed to check org name uniqueness", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}

		if !isUnique {
			existingOrg, err := s.queries.GetOrgByName(ctx, r.GetName())
			if err != nil || existingOrg.ID != r.GetOrgId() {
				slog.WarnContext(ctx, "org name already exists", "name", r.GetName())
				return nil, connect.NewError(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
			}
		}

		org, err := s.queries.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
			ID:   r.GetOrgId(),
			Name: r.GetName(),
		})
		if err != nil {
			slog.ErrorContext(ctx, "failed to update org", "error", err)
			return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
		}

		return connect.NewResponse(&orgv1.Organization{
			Id:        org.ID,
			Name:      org.Name,
			CreatedBy: org.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
			UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
		}), nil
	}

	// If no updates, just return the org
	org, err := s.queries.GetOrgByID(ctx, r.GetOrgId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to get org", "error", err)
		return nil, connect.NewError(connect.CodeNotFound, ErrOrgNotFound)
	}

	return connect.NewResponse(&orgv1.Organization{
		Id:        org.ID,
		Name:      org.Name,
		CreatedBy: org.CreatedBy,
		CreatedAt: timeutil.ParsePostgresTimestamp(org.CreatedAt.Time),
		UpdatedAt: timeutil.ParsePostgresTimestamp(org.UpdatedAt.Time),
	}), nil
}

// DeleteOrg deletes an organization
func (s *OrgServer) DeleteOrg(
	ctx context.Context,
	req *connect.Request[orgv1.DeleteOrgRequest],
) (*connect.Response[emptypb.Empty], error) {
	r := req.Msg

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.DeleteOrg, r.GetOrgId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete org", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	hasResources, err := s.queries.OrgHasWorkspacesWithResources(ctx, r.GetOrgId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check for resources in workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasResources {
		slog.WarnContext(ctx, "org has workspaces with resources", "orgId", r.GetOrgId())
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithResources)
	}

	err = s.queries.DeleteOrg(ctx, r.GetOrgId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete org", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&emptypb.Empty{}), nil
}

// ListOrgUsers lists users in an organization
func (s *OrgServer) ListOrgUsers(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgUsersRequest],
) (*connect.Response[orgv1.ListOrgUsersResponse], error) {
	// TODO: Implement authorization check for listing org users
	// TODO: Implement database query to get org users
	// For now, return empty list
	return connect.NewResponse(&orgv1.ListOrgUsersResponse{
		Users:      []*orgv1.User{},
		TotalCount: 0,
	}), nil
}

// ListOrgWorkspaces lists workspaces in an organization
func (s *OrgServer) ListOrgWorkspaces(
	ctx context.Context,
	req *connect.Request[orgv1.ListOrgWorkspacesRequest],
) (*connect.Response[orgv1.ListOrgWorkspacesResponse], error) {
	r := req.Msg

	// Check authorization
	if err := s.machine.VerifyWithGivenEntityScopes(ctx, ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope), actions.New(actions.ListWorkspaces, r.GetOrgId())); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// Get workspaces for org
	workspaces, err := s.queries.ListWorkspacesInOrg(ctx, r.GetOrgId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to list workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	workspaceSummaries := make([]*orgv1.WorkspaceSummary, len(workspaces))
	for i, ws := range workspaces {
		workspaceSummaries[i] = &orgv1.WorkspaceSummary{
			Id:        ws.ID,
			Name:      ws.Name,
			CreatedBy: ws.CreatedBy,
			CreatedAt: timeutil.ParsePostgresTimestamp(ws.CreatedAt.Time),
		}
	}

	return connect.NewResponse(&orgv1.ListOrgWorkspacesResponse{
		Workspaces:  workspaceSummaries,
		TotalCount: int64(len(workspaces)),
	}), nil
}
