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
	"github.com/team-loco/loco/api/reqctx"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrOrgNotFound                   = rpcerr.NewCoded(connect.CodeNotFound, "organization not found")
	ErrOrgNameRequired               = rpcerr.NewCoded(connect.CodeInvalidArgument, "organization name is required")
	ErrOrgHasWorkspacesWithResources = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"organization has workspaces with resources",
	)
	ErrNotOrgMember = rpcerr.NewCoded(connect.CodeFailedPrecondition,
		"user is not a member of the workspace's organization; add them to the organization first",
	)
	ErrRemoveSelfFromOrg = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"you cannot remove yourself from the organization",
	)
	errOrgMemberNotFound = rpcerr.NewCoded(connect.CodeNotFound, "organization member not found")
	errLastOrgAdmin      = rpcerr.NewCoded(connect.CodeFailedPrecondition, "organization must keep at least one admin")
	errInvalidOrgRole    = rpcerr.NewCoded(connect.CodeInvalidArgument, "role must be Admin or Member")

	errCreateIfMissingForOtherUser = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"create_if_missing requires user_id to be the caller",
	)
)

const firstWorkspaceName = "default"

// OrgServer implements the OrgService gRPC server
type OrgServer struct {
	db          *pgxpool.Pool
	queries     genDb.Querier
	authz       *authz.Authorizer
	invitations InvitationConfig
}

// NewOrgServer creates a new OrgServer instance
func NewOrgServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	invitations InvitationConfig,
) *OrgServer {
	return &OrgServer{
		db:          db,
		queries:     queries,
		invitations: invitations,
		authz:       authz.New(db, queries),
	}
}

// CreateOrg creates a new organization
func (s *OrgServer) CreateOrg(
	ctx context.Context,
	req *orgv1.CreateOrgRequest,
) (*orgv1.CreateOrgResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	// Only a user can create an organization, and only with user:write on itself.
	if p.Entity.Type != genDb.EntityTypeUser {
		return nil, rpcerr.New(connect.CodePermissionDenied, ErrImproperUsage)
	}
	if err = check(ctx, s.authz, p, actions.New(actions.CreateOrg, p.Entity.ID)); err != nil {
		return nil, err
	}
	user, err := s.queries.GetUserByID(ctx, p.Entity.ID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	orgName := req.GetName()
	if orgName == "" {
		orgName = defaultOrgName(user)
	}

	var org genDb.Organization
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var createErr error
		org, createErr = createOrgInTx(ctx, qtx, p.Entity.ID, orgName)
		return createErr
	})
	if err != nil {
		return nil, err
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
		return genDb.Organization{}, fmt.Errorf("create organization: %w", err)
	}

	err = authz.ApplyRoles(ctx, qtx, userID, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeOrganization, EntityID: org.ID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		return genDb.Organization{}, fmt.Errorf("grant roles on organization %s: %w", org.ID, err)
	}

	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.OrgCreated,
		OrgID:       new(org.ID),
		SubjectType: events.SubjectOrg,
		SubjectID:   new(org.ID),
		Data:        map[string]any{events.FieldName: org.Name},
	}); err != nil {
		return genDb.Organization{}, err
	}
	return org, nil
}

// GetOrg retrieves an organization by ID
func (s *OrgServer) GetOrg(
	ctx context.Context,
	req *orgv1.GetOrgRequest,
) (*orgv1.GetOrgResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	org, err := s.queries.GetOrgByID(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrgNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}

	// Return NotFound (not PermissionDenied) to prevent org-existence probing. An organization
	// check never reads the database, so every error here is a denial.
	if err := s.authz.Check(ctx, p.Scopes, actions.New(actions.GetOrg, org.ID)); err != nil {
		return nil, ErrOrgNotFound
	}

	return &orgv1.GetOrgResponse{Organization: orgToProto(org)}, nil
}

// ListUserOrgs lists organizations for a user
func (s *OrgServer) ListUserOrgs(
	ctx context.Context,
	req *orgv1.ListUserOrgsRequest,
) (*orgv1.ListUserOrgsResponse, error) {
	userID, err := parseID("user_id", req.GetUserId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.ListUserOrgs, userID)); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	if req.GetCreateIfMissing() {
		if err = s.createFirstOrg(ctx, p, userID); err != nil {
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
		return nil, fmt.Errorf("list organizations: %w", err)
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

func (s *OrgServer) createFirstOrg(ctx context.Context, p reqctx.Principal, userID uuid.UUID) error {
	if p.Entity.Type != genDb.EntityTypeUser || p.Entity.ID != userID {
		return errCreateIfMissingForOtherUser
	}
	return withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, err := qtx.LockUserForMembership(ctx, userID); err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		existing, err := qtx.ListOrgsForUser(ctx, genDb.ListOrgsForUserParams{UserID: userID, Limit: 1})
		if err != nil {
			return fmt.Errorf("list organizations: %w", err)
		}
		if len(existing) > 0 {
			return nil
		}
		if authzErr := check(ctx, s.authz, p, actions.New(actions.CreateOrg, userID)); authzErr != nil {
			return authzErr
		}
		user, err := qtx.GetUserByID(ctx, userID)
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		org, err := createOrgInTx(ctx, qtx, userID, defaultOrgName(user))
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
}

func defaultOrgName(user genDb.User) string {
	name := derefString(user.Name)
	if name == "" {
		name = user.Email
	}
	return name + "'s Org"
}

// UpdateOrg updates an organization
func (s *OrgServer) UpdateOrg(
	ctx context.Context,
	req *orgv1.UpdateOrgRequest,
) (*orgv1.UpdateOrgResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.UpdateOrg, orgID); err != nil {
		return nil, err
	}

	message := req.ProtoReflect()
	rename := message.Has(message.Descriptor().Fields().ByName(updateNameField))
	if rename && req.GetName() == "" {
		return nil, ErrOrgNameRequired
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if rename {
			_, updateErr := qtx.UpdateOrgName(ctx, genDb.UpdateOrgNameParams{
				ID:   orgID,
				Name: req.GetName(),
			})
			if errors.Is(updateErr, pgx.ErrNoRows) {
				return ErrOrgNotFound
			}
			if updateErr != nil {
				return fmt.Errorf("rename organization: %w", updateErr)
			}
		}
		if !rename {
			if _, loadErr := qtx.GetOrgByID(ctx, orgID); errors.Is(loadErr, pgx.ErrNoRows) {
				return ErrOrgNotFound
			} else if loadErr != nil {
				return fmt.Errorf("get organization: %w", loadErr)
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
		return nil, err
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
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.DeleteOrg, orgID); err != nil {
		return nil, err
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := qtx.LockOrgForDeletion(ctx, orgID); lockErr != nil {
			return fmt.Errorf("lock organization: %w", lockErr)
		}
		if lockErr := qtx.LockOrgWorkspacesForDeletion(ctx, orgID); lockErr != nil {
			return fmt.Errorf("lock organization workspaces: %w", lockErr)
		}
		hasResources, queryErr := qtx.OrgHasWorkspacesWithResources(ctx, orgID)
		if queryErr != nil {
			return fmt.Errorf("check organization resources: %w", queryErr)
		}
		if hasResources {
			return ErrOrgHasWorkspacesWithResources
		}
		if deleteErr := qtx.DeleteOrg(ctx, orgID); deleteErr != nil {
			return fmt.Errorf("delete organization: %w", deleteErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgDeleted,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
		})
	})
	if err != nil {
		return nil, err
	}

	return &orgv1.DeleteOrgResponse{}, nil
}

func (s *OrgServer) ListOrgMembers(
	ctx context.Context,
	req *orgv1.ListOrgMembersRequest,
) (*orgv1.ListOrgMembersResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListOrgMembers, orgID); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	rows, err := s.queries.ListOrgMembersWithDetails(ctx, genDb.ListOrgMembersWithDetailsParams{
		EntityID:      orgID,
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list organization members: %w", err)
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

func (s *OrgServer) SetOrgMemberRole(
	ctx context.Context,
	req *orgv1.SetOrgMemberRoleRequest,
) (*orgv1.SetOrgMemberRoleResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	userID, err := parseID("user_id", req.GetUserId())
	if err != nil {
		return nil, err
	}
	var scopes []genDb.Scope
	switch req.GetRole() {
	case orgv1.OrgRole_ORG_ROLE_ADMIN:
		scopes = []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin}
	case orgv1.OrgRole_ORG_ROLE_MEMBER:
		scopes = []genDb.Scope{genDb.ScopeMember}
	default:
		return nil, errInvalidOrgRole
	}
	if err = authorize(ctx, s.authz, actions.SetOrgMemberRole, orgID); err != nil {
		return nil, err
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if err = s.lockOrgMembership(ctx, qtx, orgID, actions.SetOrgMemberRole); err != nil {
			return err
		}
		if _, lockErr := qtx.LockUserForMembership(ctx, userID); errors.Is(lockErr, pgx.ErrNoRows) {
			return errOrgMemberNotFound
		} else if lockErr != nil {
			return fmt.Errorf("lock user: %w", lockErr)
		}
		member, memberErr := qtx.IsOrgMember(ctx, genDb.IsOrgMemberParams{UserID: userID, OrgID: orgID})
		if memberErr != nil {
			return fmt.Errorf("check organization membership: %w", memberErr)
		}
		if !member {
			return errOrgMemberNotFound
		}
		if req.GetRole() == orgv1.OrgRole_ORG_ROLE_MEMBER {
			if err = keepOrgAdmin(ctx, qtx, orgID, userID); err != nil {
				return err
			}
		}
		// Add first so membership never disappears while replacing the organization scopes.
		for _, scope := range scopes {
			if err = qtx.AddUserScope(
				ctx,
				genDb.AddUserScopeParams{
					UserID:     userID,
					EntityType: genDb.EntityTypeOrganization,
					EntityID:   orgID,
					Scope:      scope,
				},
			); err != nil {
				return fmt.Errorf("add organization scope: %w", err)
			}
		}
		for _, scope := range []genDb.Scope{genDb.ScopeMember, genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
			if slices.Contains(scopes, scope) {
				continue
			}
			if err = qtx.RemoveUserScope(
				ctx,
				genDb.RemoveUserScopeParams{
					UserID:     userID,
					EntityType: genDb.EntityTypeOrganization,
					EntityID:   orgID,
					Scope:      scope,
				},
			); err != nil {
				return fmt.Errorf("remove organization scope: %w", err)
			}
		}
		return events.Record(
			ctx,
			qtx,
			events.Event{
				Type:        events.OrgMemberRoleChanged,
				OrgID:       new(orgID),
				SubjectType: events.SubjectUser,
				SubjectID:   new(userID),
				Data:        map[string]any{events.FieldScopes: scopes},
			},
		)
	})
	if err != nil {
		return nil, err
	}
	return &orgv1.SetOrgMemberRoleResponse{}, nil
}

func (s *OrgServer) lockOrgMembership(
	ctx context.Context,
	qtx *genDb.Queries,
	orgID uuid.UUID,
	action actions.Action,
) error {
	// Serialize removals and demotions before counting admins or trusting the caller's scopes.
	if _, err := qtx.LockOrgForMembership(ctx, orgID); errors.Is(err, pgx.ErrNoRows) {
		return ErrOrgNotFound
	} else if err != nil {
		return fmt.Errorf("lock organization: %w", err)
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return err
	}
	if p.Entity.Type == genDb.EntityTypeUser {
		fresh, err := authz.New(s.db, qtx).UserScopes(ctx, p.Entity.ID)
		if err != nil {
			return fmt.Errorf("reload caller scopes: %w", err)
		}
		p.Scopes = fresh
		return check(ctx, s.authz, p, actions.New(action, orgID))
	}
	return nil
}

func keepOrgAdmin(ctx context.Context, qtx *genDb.Queries, orgID, userID uuid.UUID) error {
	admins, err := qtx.GetUsersWithScopeOnEntity(
		ctx,
		genDb.GetUsersWithScopeOnEntityParams{
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   orgID,
			Scope:      genDb.ScopeAdmin,
		},
	)
	if err != nil {
		return fmt.Errorf("list organization admins: %w", err)
	}
	if len(admins) == 1 && admins[0] == userID {
		return errLastOrgAdmin
	}
	return nil
}

func (s *OrgServer) RemoveOrgMember(
	ctx context.Context,
	req *orgv1.RemoveOrgMemberRequest,
) (*orgv1.RemoveOrgMemberResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.RemoveOrgMember, orgID)); err != nil {
		return nil, err
	}

	userID := uuid.MustParse(req.GetUserId())
	if userID == p.Entity.ID {
		return nil, ErrRemoveSelfFromOrg
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if err = s.lockOrgMembership(ctx, qtx, orgID, actions.RemoveOrgMember); err != nil {
			return err
		}
		if err = keepOrgAdmin(ctx, qtx, orgID, userID); err != nil {
			return err
		}
		_, lockErr := qtx.LockUserForMembership(ctx, userID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if lockErr != nil {
			return fmt.Errorf("lock user: %w", lockErr)
		}
		if removeErr := qtx.RemoveOrgMember(ctx, genDb.RemoveOrgMemberParams{
			UserID: userID,
			OrgID:  orgID,
		}); removeErr != nil {
			return fmt.Errorf("remove organization member: %w", removeErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgMemberRemoved,
			OrgID:       new(orgID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, err
	}

	return &orgv1.RemoveOrgMemberResponse{}, nil
}

func orgToProto(org genDb.Organization) *orgv1.Organization {
	return &orgv1.Organization{
		Id:               org.ID.String(),
		Name:             org.Name,
		CreatedBy:        org.CreatedBy.String(),
		CreatedAt:        timestamppb.New(org.CreatedAt),
		UpdatedAt:        timestamppb.New(org.UpdatedAt),
		SuspendedAt:      optionalTimestamp(org.SuspendedAt),
		SuspensionReason: derefString(org.SuspensionReason),
	}
}

// derefString safely dereferences a string pointer, returning an empty string if nil.
func derefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}
