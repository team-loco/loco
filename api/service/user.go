package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var ErrUserNotFound = rpcerr.NewCoded(connect.CodeNotFound, "user not found")

// UserServer implements the UserService gRPC server
type UserServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	tvm     *tvm.VendingMachine
}

// NewUserServer creates a new UserServer instance
func NewUserServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	vendingMachine *tvm.VendingMachine,
) *UserServer {
	return &UserServer{db: db, queries: queries, tvm: vendingMachine, authz: authz.New(queries)}
}

// GetUser retrieves a user by ID or email
func (s *UserServer) GetUser(
	ctx context.Context,
	req *userv1.GetUserRequest,
) (*userv1.GetUserResponse, error) {
	var targetUserID string
	var err error

	switch key := req.GetKey().(type) {
	case *userv1.GetUserRequest_UserId:
		targetUserID = key.UserId
	case *userv1.GetUserRequest_Email:
		dbUser, getErr := s.queries.GetUserByEmail(ctx, key.Email)
		if errors.Is(getErr, pgx.ErrNoRows) {
			// Same NotFound as a denied lookup, so a caller cannot probe which emails exist.
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if getErr != nil {
			return nil, fmt.Errorf("get user by email: %w", getErr)
		}
		targetUserID = dbUser.ID.String()
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest)
	}

	targetID, err := parseID("user_id", targetUserID)
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.GetUser, targetID); err != nil {
		// NotFound, not PermissionDenied, so a caller cannot probe which users exist.
		if connect.CodeOf(err) == connect.CodePermissionDenied {
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		return nil, err
	}

	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	var user *userv1.User
	if s.authz.ReadableEntities(p.Scopes).System {
		row, getErr := s.queries.GetUserWithActivity(ctx, targetID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if getErr != nil {
			return nil, fmt.Errorf("get user: %w", getErr)
		}
		user = dbUserToProto(row.User)
		showOperatorFields(user, row.User.Status, row.LastActiveAt)
	} else {
		dbUser, getErr := s.getDBUser(ctx, targetID)
		if getErr != nil {
			return nil, getErr
		}
		user = dbUserToProto(dbUser)
	}

	return &userv1.GetUserResponse{User: user}, nil
}

// WhoAmI retrieves the current authenticated user
func (s *UserServer) WhoAmI(
	ctx context.Context,
	_ *userv1.WhoAmIRequest,
) (*userv1.WhoAmIResponse, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if p.Entity.Type != genDb.EntityTypeUser {
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied)
	}
	if err = check(ctx, s.authz, p, actions.New(actions.GetCurrentUser, p.Entity.ID)); err != nil {
		return nil, err
	}

	dbUser, err := s.getDBUser(ctx, p.Entity.ID)
	if err != nil {
		return nil, err
	}
	user := dbUserToProto(dbUser)
	slog.InfoContext(ctx, "returning user")

	// API tokens carry no provider login and claim no invitations.
	login, _ := auth.LoginFrom(ctx)
	return &userv1.WhoAmIResponse{User: user, ClaimedOrgs: claimedOrgsProto(login.ClaimedOrgs)}, nil
}

// UpdateUser updates user information
func (s *UserServer) UpdateUser(
	ctx context.Context,
	req *userv1.UpdateUserRequest,
) (*userv1.UpdateUserResponse, error) {
	userID, err := parseID("user_id", req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.UpdateUser, userID); err != nil {
		return nil, err
	}

	message := req.ProtoReflect()
	fields := message.Descriptor().Fields()
	params := genDb.UpdateUserProfileParams{ID: userID}
	if message.Has(fields.ByName(updateNameField)) {
		params.Name = new(req.GetName())
	}
	if message.Has(fields.ByName(updateAvatarField)) {
		params.AvatarUrl = new(req.GetAvatarUrl())
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, updateErr := qtx.UpdateUserProfile(ctx, params); updateErr != nil {
			return fmt.Errorf("update user profile: %w", updateErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.UserUpdated,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, err
	}

	return &userv1.UpdateUserResponse{UserId: req.GetUserId()}, nil
}

// ListUsers lists all users with pagination
func (s *UserServer) ListUsers(
	ctx context.Context,
	req *userv1.ListUsersRequest,
) (*userv1.ListUsersResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.ListUsers); err != nil {
		return nil, err
	}

	pageSize := normalizePageSize(req.GetPageSize())

	cursor, cursorErr := decodePositionCursor(req.GetPageToken())
	if cursorErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidPageToken).WithCause(cursorErr)
	}

	query := likePattern(req.GetQuery())
	rows, err := s.queries.ListUsers(ctx, genDb.ListUsersParams{
		Query:         query,
		Limit:         pageSize + 1,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	rows, token := trimPage(rows, pageSize, func(row genDb.ListUsersRow) (uuid.UUID, time.Time) {
		return row.User.ID, row.User.CreatedAt
	})

	var users []*userv1.User
	for _, row := range rows {
		user := dbUserToProto(row.User)
		showOperatorFields(user, row.User.Status, row.LastActiveAt)
		users = append(users, user)
	}
	total, err := totalCount(ctx, req.GetIncludeTotalCount(), func(ctx context.Context, limit int32) (int64, error) {
		return s.queries.CountUsers(ctx, genDb.CountUsersParams{Query: query, Limit: limit})
	})
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}

	return &userv1.ListUsersResponse{
		Users:         users,
		NextPageToken: token,
		TotalCount:    total,
	}, nil
}

// DeleteUser removes the account and its access after preserving each organization's administration.
func (s *UserServer) DeleteUser(
	ctx context.Context,
	req *userv1.DeleteUserRequest,
) (*userv1.DeleteUserResponse, error) {
	userID, err := parseID("user_id", req.GetUserId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.DeleteUser, userID); err != nil {
		return nil, err
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		lockedCtx, lockErr := lockUserMutation(ctx, qtx, userID)
		if lockErr != nil {
			return lockErr
		}
		if accessErr := authorize(lockedCtx, authz.New(qtx), actions.DeleteUser, userID); accessErr != nil {
			return accessErr
		}
		orgs, lockErr := qtx.LockUserAdminOrganizations(lockedCtx, userID)
		if lockErr != nil {
			return fmt.Errorf("lock user organizations: %w", lockErr)
		}
		for _, org := range orgs {
			needsSuccessor, checkErr := qtx.OrgAdminHasNoActiveSuccessor(
				lockedCtx,
				genDb.OrgAdminHasNoActiveSuccessorParams{OrgID: org.ID, UserID: userID},
			)
			if checkErr != nil {
				return fmt.Errorf("check active organization admins: %w", checkErr)
			}
			if needsSuccessor {
				return rpcerr.NewCoded(connect.CodeFailedPrecondition,
					fmt.Sprintf("organization %q must keep at least one active admin", org.Name))
			}
		}
		data := map[string]any{}
		identity, identityErr := qtx.GetIdentityForUser(lockedCtx, userID)
		switch {
		case identityErr == nil:
			data[events.FieldIdentity] = map[string]string{
				events.FieldIssuer:  identity.Issuer,
				events.FieldSubject: identity.Subject,
			}
		case !errors.Is(identityErr, pgx.ErrNoRows):
			return fmt.Errorf("get user identity: %w", identityErr)
		}
		if recordErr := events.Record(lockedCtx, qtx, events.Event{
			Type:        events.UserDeleted,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
			Data:        data,
		}); recordErr != nil {
			return recordErr
		}
		if deleteErr := qtx.DeleteUser(lockedCtx, userID); deleteErr != nil {
			return fmt.Errorf("delete user: %w", deleteErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &userv1.DeleteUserResponse{}, nil
}

// SetUserStatus suspends or reinstates a user
func (s *UserServer) SetUserStatus(
	ctx context.Context,
	req *userv1.SetUserStatusRequest,
) (*userv1.SetUserStatusResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.SetUserStatus); err != nil {
		return nil, err
	}

	var userID uuid.UUID
	switch key := req.GetKey().(type) {
	case *userv1.SetUserStatusRequest_UserId:
		userID = uuid.MustParse(key.UserId)
	case *userv1.SetUserStatusRequest_Email:
		dbUser, getErr := s.queries.GetUserByEmail(ctx, key.Email)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if getErr != nil {
			return nil, fmt.Errorf("get user by email: %w", getErr)
		}
		userID = dbUser.ID
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest)
	}

	caller, callerErr := principalFrom(ctx)
	if callerErr != nil {
		return nil, callerErr
	}
	if caller.UserID == userID {
		return nil, connect.NewError(connect.CodePermissionDenied, "you cannot change your own account status")
	}

	status, eventType := genDb.UserStatusActive, events.UserReinstated
	if req.GetStatus() == userv1.UserStatus_USER_STATUS_SUSPENDED {
		status, eventType = genDb.UserStatusSuspended, events.UserSuspended
	}
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		lockedCtx, lockErr := lockUserMutation(ctx, qtx, userID)
		if lockErr != nil {
			return lockErr
		}
		if err := authorizeSystem(lockedCtx, authz.New(qtx), actions.SetUserStatus); err != nil {
			return err
		}
		if _, err := qtx.LockUserAdminOrganizations(lockedCtx, userID); err != nil {
			return fmt.Errorf("lock user organizations: %w", err)
		}
		_, setErr := qtx.SetUserStatus(lockedCtx, genDb.SetUserStatusParams{ID: userID, Status: status})
		if errors.Is(setErr, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		if setErr != nil {
			return fmt.Errorf("set user status: %w", setErr)
		}
		return events.Record(lockedCtx, qtx, events.Event{
			Type:        eventType,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, err
	}
	return &userv1.SetUserStatusResponse{}, nil
}

// Logout revokes the caller's Loco session token. Provider tokens are left to the provider.
func (s *UserServer) Logout(
	ctx context.Context,
	_ *userv1.LogoutRequest,
) (*userv1.LogoutResponse, error) {
	res := &userv1.LogoutResponse{}

	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if !tvm.IsLocoToken(p.Token) {
		return res, nil
	}
	err = s.tvm.Revoke(ctx, p.Token)
	if err != nil {
		return nil, fmt.Errorf("revoke session token: %w", err)
	}

	slog.InfoContext(ctx, "user logged out")
	return res, nil
}

// Helper methods

func (s *UserServer) getDBUser(ctx context.Context, id uuid.UUID) (genDb.User, error) {
	user, err := s.queries.GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
	}
	if err != nil {
		return genDb.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

// showOperatorFields adds the account state and last session activity that only system readers see.
func showOperatorFields(user *userv1.User, status genDb.UserStatus, lastActive *time.Time) {
	user.Status = userv1.UserStatus_USER_STATUS_ACTIVE
	if status == genDb.UserStatusSuspended {
		user.Status = userv1.UserStatus_USER_STATUS_SUSPENDED
	}
	user.LastActiveAt = optionalTimestamp(lastActive)
}

func dbUserToProto(user genDb.User) *userv1.User {
	return &userv1.User{
		Id:        user.ID.String(),
		Email:     user.Email,
		Name:      derefString(user.Name),
		AvatarUrl: derefString(user.AvatarUrl),
		CreatedAt: timestamppb.New(user.CreatedAt),
		UpdatedAt: timestamppb.New(user.UpdatedAt),
	}
}
