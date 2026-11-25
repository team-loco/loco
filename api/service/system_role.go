package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errAlreadyGranted = rpcerr.NewCoded(connect.CodeAlreadyExists, "user already has this system role grant")
	errNoRoleGrant    = rpcerr.NewCoded(connect.CodeNotFound, "user has no granted system role")
	errLastAdmin      = rpcerr.NewCoded(connect.CodeFailedPrecondition, "cannot remove the last system admin")
	errConfiguredRole = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"this user claimed an email in LOCO_SYSTEM_ADMINS or LOCO_SYSTEM_READERS; "+
			"change the configuration to lower or remove that role",
	)
)

type SystemRoleServer struct {
	pool    *pgxpool.Pool
	queries *genDb.Queries
	authz   *authz.Authorizer
}

func NewSystemRoleServer(pool *pgxpool.Pool, queries *genDb.Queries) *SystemRoleServer {
	return &SystemRoleServer{pool: pool, queries: queries, authz: authz.New(queries)}
}

func (s *SystemRoleServer) ListSystemRoles(
	ctx context.Context,
	_ *adminv1.ListSystemRolesRequest,
) (*adminv1.ListSystemRolesResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListSystemRoles); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSystemRoleHolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list system role holders: %w", err)
	}
	unclaimed, err := s.queries.ListUnclaimedSystemRoleEmails(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unclaimed system role emails: %w", err)
	}
	response := &adminv1.ListSystemRolesResponse{}
	for _, row := range rows {
		response.Holders = append(response.Holders, &adminv1.SystemRoleHolder{
			UserId:         row.ID.String(),
			Email:          row.Email,
			Name:           derefString(row.Name),
			LastActiveAt:   optionalTimestamp(row.LastActiveAt),
			Issuer:         derefString(row.Issuer),
			Subject:        derefString(row.Subject),
			ConfiguredRole: systemRoleToProto(row.ConfiguredRole),
			GrantedRole:    systemRoleToProto(row.GrantedRole),
		})
	}
	for _, row := range unclaimed {
		response.Unclaimed = append(response.Unclaimed, &adminv1.UnclaimedEmail{
			Email: row.Email, Role: systemRoleToProto(&row.Role),
		})
	}
	return response, nil
}

func (s *SystemRoleServer) GrantSystemRole(
	ctx context.Context,
	req *adminv1.GrantSystemRoleRequest,
) (*adminv1.GrantSystemRoleResponse, error) {
	role := genDb.SystemRoleRead
	if req.GetRole() == adminv1.SystemRole_SYSTEM_ROLE_ADMIN {
		role = genDb.SystemRoleAdmin
	}
	err := s.changeRole(ctx, uuid.MustParse(req.GetUserId()), func(
		ctx context.Context, qtx *genDb.Queries, user genDb.User, sources genDb.GetSystemRoleSourcesRow,
	) (genDb.SystemRole, error) {
		if sources.GrantedRole != nil && *sources.GrantedRole == role {
			return "", errAlreadyGranted
		}
		if auth.IsSystemAdmin(sources.ConfiguredRole) && role != genDb.SystemRoleAdmin {
			return "", errConfiguredRole
		}
		if err := qtx.SetSystemRoleGrant(ctx, genDb.SetSystemRoleGrantParams{UserID: user.ID, Role: role}); err != nil {
			return "", fmt.Errorf("record system role grant: %w", err)
		}
		return role, nil
	}, events.SystemRoleGranted)
	if err != nil {
		return nil, err
	}
	return &adminv1.GrantSystemRoleResponse{}, nil
}

func (s *SystemRoleServer) RevokeSystemRole(
	ctx context.Context,
	req *adminv1.RevokeSystemRoleRequest,
) (*adminv1.RevokeSystemRoleResponse, error) {
	err := s.changeRole(ctx, uuid.MustParse(req.GetUserId()), func(
		ctx context.Context, qtx *genDb.Queries, user genDb.User, sources genDb.GetSystemRoleSourcesRow,
	) (genDb.SystemRole, error) {
		if sources.GrantedRole == nil && sources.ConfiguredRole != nil {
			return "", errConfiguredRole
		}
		if sources.GrantedRole == nil {
			return "", errNoRoleGrant
		}
		if err := qtx.RemoveSystemRoleGrant(ctx, user.ID); err != nil {
			return "", fmt.Errorf("remove system role grant: %w", err)
		}
		return *sources.GrantedRole, nil
	}, events.SystemRoleRevoked)
	if err != nil {
		return nil, err
	}
	return &adminv1.RevokeSystemRoleResponse{}, nil
}

// roleChange edits userID's grant and returns the role the event records.
type roleChange func(
	ctx context.Context, qtx *genDb.Queries, user genDb.User, sources genDb.GetSystemRoleSourcesRow,
) (genDb.SystemRole, error)

// changeRole runs change under the user and admin locks, applies the resulting role and refuses
// a change that leaves the installation without a system admin.
func (s *SystemRoleServer) changeRole(
	ctx context.Context,
	userID uuid.UUID,
	change roleChange,
	eventType string,
) error {
	if err := authorizeSystem(ctx, s.authz, actions.ManageSystemRoles); err != nil {
		return err
	}
	return withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		lockedCtx, lockErr := lockUserMutation(ctx, qtx, userID)
		if lockErr != nil {
			return lockErr
		}
		if err := authorizeSystem(lockedCtx, authz.New(qtx), actions.ManageSystemRoles); err != nil {
			return err
		}
		user, lookupErr := qtx.GetUserByID(lockedCtx, userID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if lookupErr != nil {
			return fmt.Errorf("look up user: %w", lookupErr)
		}
		admins, lockErr := qtx.LockSystemAdmins(lockedCtx)
		if lockErr != nil {
			return fmt.Errorf("lock system admins: %w", lockErr)
		}
		sources, sourceErr := qtx.GetSystemRoleSources(lockedCtx, userID)
		if sourceErr != nil {
			return fmt.Errorf("look up system roles: %w", sourceErr)
		}
		recorded, err := change(lockedCtx, qtx, user, sources)
		if err != nil {
			return err
		}
		role, err := auth.ApplySystemRole(lockedCtx, qtx, userID)
		if err != nil {
			return err
		}
		lastAdmin := len(admins) == 1 && admins[0] == userID
		if lastAdmin && !auth.IsSystemAdmin(role) {
			return errLastAdmin
		}
		return recordRoleChange(lockedCtx, qtx, eventType, user, recorded)
	})
}

func recordRoleChange(
	ctx context.Context, qtx *genDb.Queries, eventType string, user genDb.User, role genDb.SystemRole,
) error {
	if err := events.Record(ctx, qtx, events.Event{
		Type:        eventType,
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data: map[string]any{
			events.FieldEmail: user.Email, events.FieldRole: role, events.FieldSource: events.SourceGrant,
		},
	}); err != nil {
		return fmt.Errorf("record %s: %w", eventType, err)
	}
	return nil
}

func systemRoleToProto(role *genDb.SystemRole) adminv1.SystemRole {
	switch {
	case role == nil:
		return adminv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
	case *role == genDb.SystemRoleAdmin:
		return adminv1.SystemRole_SYSTEM_ROLE_ADMIN
	default:
		return adminv1.SystemRole_SYSTEM_ROLE_READ
	}
}
