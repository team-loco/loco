package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrOrgNotFound                   = errors.New("organization not found")
	ErrOrgNameRequired               = errors.New("organization name is required")
	ErrOrgNameNotUnique              = errors.New("organization name already exists")
	ErrOrgHasWorkspacesWithResources = errors.New("organization has workspaces with resources")
	ErrNotOrgMember                  = errors.New(
		"user is not a member of the workspace's organization; add them to the organization first",
	)
	ErrRemoveSelfFromOrg           = errors.New("you cannot remove yourself from the organization")
	errCreateIfMissingForOtherUser = errors.New("create_if_missing requires user_id to be the caller")
)

const (
	firstWorkspaceName      = "default"
	firstOrgNameSuffix      = "-org"
	firstOrgNameAttempts    = 4
	firstOrgRandomSuffixLen = 4
	// maxOrgNameLen matches the max_len of the organization name in org.proto.
	maxOrgNameLen = 100
)

var emailNameReplacer = strings.NewReplacer("@", "-", ".", "-", "+", "-")

// OrgServer implements the OrgService gRPC server
type OrgServer struct {
	db          *pgxpool.Pool
	queries     genDb.Querier
	authz       *authz.Authorizer
	machine     *tvm.VendingMachine
	invitations InvitationConfig
}

// NewOrgServer creates a new OrgServer instance
func NewOrgServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	invitations InvitationConfig,
) *OrgServer {
	return &OrgServer{
		db:          db,
		queries:     queries,
		machine:     machine,
		invitations: invitations,
		authz:       authz.New(db, queries),
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

	org, err := createOrgInTx(ctx, genDb.New(tx), entity.ID, orgName)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit organization creation", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	return &orgv1.CreateOrgResponse{
		OrgId: org.ID.String(),
	}, nil
}

func createOrgInTx(ctx context.Context, qtx *genDb.Queries, userID uuid.UUID, name string) (genDb.Organization, error) {
	org, err := qtx.CreateOrg(ctx, genDb.CreateOrgParams{
		Name:      name,
		CreatedBy: userID,
	})
	if err != nil {
		if isPgConstraintViolation(err) {
			return genDb.Organization{}, rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
		}
		slog.ErrorContext(ctx, "failed to create organization", "error", err)
		return genDb.Organization{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	err = authz.ApplyRoles(ctx, qtx, userID, []genDb.EntityScope{
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
			userID.String(),
		)
		return genDb.Organization{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.OrgCreated,
		OrgID:       new(org.ID),
		SubjectType: events.SubjectOrg,
		SubjectID:   new(org.ID),
		Data:        map[string]any{events.FieldName: org.Name},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record org creation", "error", err)
		return genDb.Organization{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	return org, nil
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

	return &orgv1.GetOrgResponse{Organization: orgToProto(org)}, nil
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

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	userID := uuid.MustParse(req.GetUserId())
	if req.GetCreateIfMissing() {
		if err := s.createFirstOrg(ctx, scopes, userID); err != nil {
			return nil, err
		}
	}

	orgs, err := s.queries.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{
		UserID:        userID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list orgs", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var orgResponses []*orgv1.Organization
	for _, org := range orgs {
		orgResponses = append(orgResponses, orgToProto(org))
	}

	token := nextPageToken(
		orgs,
		pageSize,
		func(row genDb.Organization) (uuid.UUID, time.Time) { return row.ID, row.CreatedAt },
	)

	return &orgv1.ListUserOrgsResponse{
		Orgs:          orgResponses,
		NextPageToken: token,
	}, nil
}

func (s *OrgServer) createFirstOrg(ctx context.Context, scopes []genDb.EntityScope, userID uuid.UUID) error {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if entity.Type != genDb.EntityTypeUser || entity.ID != userID {
		return rpcerr.New(connect.CodeInvalidArgument, errCreateIfMissingForOtherUser)
	}
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		if _, err := qtx.LockUserForMembership(ctx, userID); err != nil {
			return err
		}
		existing, err := qtx.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{UserID: userID, Limit: 1})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			return nil
		}
		if authzErr := s.authz.Check(ctx, scopes, actions.New(actions.CreateOrg, userID.String())); authzErr != nil {
			return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(authzErr)
		}
		user, err := qtx.GetUserByID(ctx, userID)
		if err != nil {
			return err
		}
		org, err := createOrgWithFreeName(ctx, tx, userID, user.Email)
		if err != nil {
			return err
		}
		_, err = createWorkspaceInTx(ctx, qtx, genDb.CreateWorkspaceParams{
			OrgID:     org.ID,
			Name:      firstWorkspaceName,
			CreatedBy: userID,
		})
		return err
	})
	if err != nil {
		return txError(ctx, "failed to create first organization", err)
	}
	return nil
}

// createOrgWithFreeName creates each candidate name in a savepoint, because a unique
// violation aborts the surrounding transaction.
func createOrgWithFreeName(ctx context.Context, tx pgx.Tx, userID uuid.UUID, email string) (genDb.Organization, error) {
	for attempt := range firstOrgNameAttempts {
		name := firstOrgName(email, attempt)
		var org genDb.Organization
		err := pgx.BeginFunc(ctx, tx, func(savepoint pgx.Tx) error {
			var createErr error
			org, createErr = createOrgInTx(ctx, genDb.New(savepoint), userID, name)
			return createErr
		})
		if err == nil {
			return org, nil
		}
		if !errors.Is(err, ErrOrgNameNotUnique) {
			return genDb.Organization{}, err
		}
	}
	return genDb.Organization{}, rpcerr.New(connect.CodeAlreadyExists, ErrOrgNameNotUnique)
}

func firstOrgName(email string, attempt int) string {
	suffix := firstOrgNameSuffix
	if attempt > 0 {
		suffix += "-" + strings.ToLower(rand.Text()[:firstOrgRandomSuffixLen])
	}
	base := []rune(emailNameReplacer.Replace(strings.ToLower(email)))
	limit := maxOrgNameLen - utf8.RuneCountInString(suffix)
	if len(base) > limit {
		base = base[:limit]
	}
	return string(base) + suffix
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

	message := req.ProtoReflect()
	rename := message.Has(message.Descriptor().Fields().ByName(updateNameField))
	if rename && req.GetName() == "" {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrOrgNameRequired)
	}
	orgID := uuid.MustParse(req.GetOrgId())
	if rename {
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
		if rename {
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
		if !rename {
			if _, loadErr := qtx.GetOrgByID(ctx, orgID); errors.Is(loadErr, pgx.ErrNoRows) {
				return rpcerr.New(connect.CodeNotFound, ErrOrgNotFound)
			} else if loadErr != nil {
				return loadErr
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

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := qtx.LockOrgForDeletion(ctx, orgID); lockErr != nil {
			return lockErr
		}
		if lockErr := qtx.LockOrgWorkspacesForDeletion(ctx, orgID); lockErr != nil {
			return lockErr
		}
		hasResources, queryErr := qtx.OrgHasWorkspacesWithResources(ctx, orgID)
		if queryErr != nil {
			return queryErr
		}
		if hasResources {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrOrgHasWorkspacesWithResources)
		}
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

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	rows, err := s.queries.ListOrgMembersWithDetails(ctx, genDb.ListOrgMembersWithDetailsParams{
		EntityID:      uuid.MustParse(req.GetOrgId()),
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
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

	token := nextPageToken(rows, pageSize, func(m genDb.ListOrgMembersWithDetailsRow) (uuid.UUID, time.Time) {
		return m.ID, m.JoinedAt
	})
	return &orgv1.ListOrgMembersResponse{
		Members:       members,
		NextPageToken: token,
	}, nil
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
		_, lockErr := qtx.LockUserForMembership(ctx, userID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if lockErr != nil {
			return lockErr
		}
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
