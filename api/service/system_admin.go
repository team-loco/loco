package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	errAlreadyGranted = rpcerr.NewCoded(connect.CodeAlreadyExists, "user already has a system admin grant")
	errNotSystemAdmin = rpcerr.NewCoded(connect.CodeNotFound, "user is not a system admin")
	errLastAdmin      = rpcerr.NewCoded(connect.CodeFailedPrecondition, "cannot revoke the last system admin")
	errConfigAdmin    = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"this admin claimed an email in LOCO_SYSTEM_ADMINS; remove the email from the configuration to revoke it",
	)
)

type SystemAdminServer struct {
	pool    *pgxpool.Pool
	queries *genDb.Queries
	authz   *authz.Authorizer
}

func NewSystemAdminServer(pool *pgxpool.Pool, queries *genDb.Queries) *SystemAdminServer {
	return &SystemAdminServer{pool: pool, queries: queries, authz: authz.New(queries)}
}

func (s *SystemAdminServer) ListSystemAdmins(
	ctx context.Context,
	_ *adminv1.ListSystemAdminsRequest,
) (*adminv1.ListSystemAdminsResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageSystemAdmins); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSystemAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("list system admins: %w", err)
	}
	unclaimed, err := s.queries.ListUnclaimedSystemAdminEmails(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unclaimed system admin emails: %w", err)
	}
	response := &adminv1.ListSystemAdminsResponse{UnclaimedEmails: unclaimed}
	for _, row := range rows {
		admin := &adminv1.SystemAdmin{
			UserId:  row.ID.String(),
			Email:   row.Email,
			Issuer:  derefString(row.Issuer),
			Subject: derefString(row.Subject),
		}
		if row.Configured {
			admin.Sources = append(admin.Sources, adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_CONFIG)
		}
		if row.Granted {
			admin.Sources = append(admin.Sources, adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_GRANT)
		}
		response.Admins = append(response.Admins, admin)
	}
	return response, nil
}

func (s *SystemAdminServer) GrantSystemAdmin(
	ctx context.Context,
	req *adminv1.GrantSystemAdminRequest,
) (*adminv1.GrantSystemAdminResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageSystemAdmins); err != nil {
		return nil, err
	}
	userID := uuid.MustParse(req.GetUserId())
	err := withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		lockedCtx, lockErr := lockUserMutation(ctx, qtx, userID)
		if lockErr != nil {
			return lockErr
		}
		if err := authorizeSystem(lockedCtx, authz.New(qtx), actions.ManageSystemAdmins); err != nil {
			return err
		}
		user, lookupErr := qtx.GetUserByID(lockedCtx, userID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if lookupErr != nil {
			return fmt.Errorf("look up user: %w", lookupErr)
		}
		added, grantErr := qtx.AddSystemAdminGrant(lockedCtx, userID)
		if grantErr != nil {
			return fmt.Errorf("record system admin grant: %w", grantErr)
		}
		if added == 0 {
			return errAlreadyGranted
		}
		if scopeErr := qtx.AddUserScope(lockedCtx, auth.SystemAdminScope(userID)); scopeErr != nil {
			return fmt.Errorf("grant system admin: %w", scopeErr)
		}
		return recordAdminChange(lockedCtx, qtx, events.SystemAdminGranted, user)
	})
	if err != nil {
		return nil, err
	}
	return &adminv1.GrantSystemAdminResponse{}, nil
}

func (s *SystemAdminServer) RevokeSystemAdmin(
	ctx context.Context,
	req *adminv1.RevokeSystemAdminRequest,
) (*adminv1.RevokeSystemAdminResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ManageSystemAdmins); err != nil {
		return nil, err
	}
	userID := uuid.MustParse(req.GetUserId())
	err := withTx(ctx, s.pool, func(qtx *genDb.Queries) error {
		lockedCtx, lockErr := lockUserMutation(ctx, qtx, userID)
		if lockErr != nil {
			return lockErr
		}
		if err := authorizeSystem(lockedCtx, authz.New(qtx), actions.ManageSystemAdmins); err != nil {
			return err
		}
		admins, lockErr := qtx.LockSystemAdmins(lockedCtx)
		if lockErr != nil {
			return fmt.Errorf("lock system admins: %w", lockErr)
		}
		if !slices.Contains(admins, userID) {
			return errNotSystemAdmin
		}
		configured, claimErr := qtx.SystemAdminClaimed(lockedCtx, &userID)
		if claimErr != nil {
			return fmt.Errorf("check system admin claim: %w", claimErr)
		}
		if configured {
			return errConfigAdmin
		}
		if len(admins) == 1 {
			return errLastAdmin
		}
		if _, removeErr := qtx.RemoveSystemAdminGrant(lockedCtx, userID); removeErr != nil {
			return fmt.Errorf("remove system admin grant: %w", removeErr)
		}
		if scopeErr := qtx.RemoveUserScope(
			lockedCtx, genDb.RemoveUserScopeParams(auth.SystemAdminScope(userID)),
		); scopeErr != nil {
			return fmt.Errorf("revoke system admin: %w", scopeErr)
		}
		user, lookupErr := qtx.GetUserByID(lockedCtx, userID)
		if lookupErr != nil {
			return fmt.Errorf("look up user: %w", lookupErr)
		}
		return recordAdminChange(lockedCtx, qtx, events.SystemAdminRevoked, user)
	})
	if err != nil {
		return nil, err
	}
	return &adminv1.RevokeSystemAdminResponse{}, nil
}

func recordAdminChange(ctx context.Context, qtx *genDb.Queries, eventType string, user genDb.User) error {
	if err := events.Record(ctx, qtx, events.Event{
		Type:        eventType,
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data:        map[string]any{events.FieldEmail: user.Email, events.FieldSource: events.SourceGrant},
	}); err != nil {
		return fmt.Errorf("record %s: %w", eventType, err)
	}
	return nil
}
