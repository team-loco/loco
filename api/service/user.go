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

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const updateAvatarField = "avatar_url"

var (
	ErrUserNotFound           = rpcerr.NewCoded(connect.CodeNotFound, "user not found")
	ErrUserAlreadyExists      = errors.New("user already exists")
	ErrEmailAlreadyRegistered = errors.New("email already registered with different provider")
	ErrInvalidRequest         = errors.New("invalid request")
	ErrUserHasActiveResources = errors.New("user owns workspaces with active resources")
	ErrUserHasOrganizations   = errors.New("user owns organizations")
)

// UserServer implements the UserService gRPC server
type UserServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	tvm     *tvm.VendingMachine
	admins  auth.Admins
}

// NewUserServer creates a new UserServer instance
func NewUserServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	vendingMachine *tvm.VendingMachine,
	admins auth.Admins,
) *UserServer {
	return &UserServer{db: db, queries: queries, tvm: vendingMachine, admins: admins, authz: authz.New(db, queries)}
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
		if getErr != nil {
			// Return NotFound regardless of reason to prevent user-existence probing by email.
			slog.WarnContext(ctx, "user not found by email")
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		targetUserID = dbUser.ID.String()
	default:
		slog.ErrorContext(ctx, "invalid request: either id or email must be provided")
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrInvalidRequest)
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

	user, err := s.getUserByID(ctx, targetUserID)
	if err != nil {
		return nil, err
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
	if err = check(ctx, s.authz, p, actions.New(actions.GetCurrentUser, p.Entity.ID)); err != nil {
		return nil, err
	}

	user, err := s.getUserByID(ctx, p.Entity.ID.String())
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "returning user")

	// API tokens carry no provider login and claim no invitations.
	login, _ := auth.LoginFrom(ctx)
	claimedOrgs := make([]*userv1.ClaimedOrg, 0, len(login.ClaimedOrgs))
	for _, org := range login.ClaimedOrgs {
		claimedOrgs = append(claimedOrgs, &userv1.ClaimedOrg{
			OrgId:       org.OrgID.String(),
			OrgName:     org.OrgName,
			InviterName: org.InviterName,
		})
	}

	return &userv1.WhoAmIResponse{User: user, ClaimedOrgs: claimedOrgs}, nil
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
			return updateErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.UserUpdated,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update user", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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

	dbUsers, err := s.queries.ListUsers(ctx, genDb.ListUsersParams{
		Limit:         pageSize,
		PageToken:     cursor.ID,
		PageCreatedAt: cursor.CreatedAt,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list users", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	var users []*userv1.User
	for _, dbUser := range dbUsers {
		users = append(users, dbUserToProto(dbUser))
	}

	token := nextPageToken(dbUsers, pageSize, func(u genDb.User) (uuid.UUID, time.Time) { return u.ID, u.CreatedAt })
	return &userv1.ListUsersResponse{
		Users:         users,
		NextPageToken: token,
	}, nil
}

// DeleteUser deletes a user (only if no active resources)
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

	var identities []genDb.Identity
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, lockErr := qtx.LockUserForMembership(ctx, userID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		if lockErr != nil {
			return fmt.Errorf("lock user: %w", lockErr)
		}
		hasWorkspaces, checkErr := qtx.CheckUserHasWorkspaces(ctx, userID)
		if checkErr != nil {
			return fmt.Errorf("check user workspaces: %w", checkErr)
		}
		if hasWorkspaces {
			slog.WarnContext(ctx, "cannot delete user with active workspace memberships", "userId", req.GetUserId())
			return rpcerr.New(connect.CodeFailedPrecondition, ErrUserHasActiveResources)
		}
		hasOrganizations, checkErr := qtx.CheckUserHasOrganizations(ctx, userID)
		if checkErr != nil {
			return fmt.Errorf("check user organizations: %w", checkErr)
		}
		if hasOrganizations {
			slog.WarnContext(ctx, "cannot delete user with owned organizations", "userId", req.GetUserId())
			return rpcerr.New(connect.CodeFailedPrecondition, ErrUserHasOrganizations)
		}
		var listErr error
		identities, listErr = qtx.ListIdentitiesForUser(ctx, userID)
		if listErr != nil {
			return fmt.Errorf("list user identities: %w", listErr)
		}
		if deleteErr := qtx.DeleteUser(ctx, userID); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.UserDeleted,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete user", err)
	}

	for _, identity := range identities {
		admin, ok := s.admins.For(identity.Issuer)
		if !ok {
			continue
		}
		if deleteErr := admin.Delete(ctx, identity.Subject); deleteErr != nil {
			slog.ErrorContext(ctx, "failed to delete identity at its provider",
				"userId", userID, "issuer", identity.Issuer, "error", deleteErr)
		}
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
		if getErr != nil {
			return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
		}
		userID = dbUser.ID
	default:
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	status, eventType := genDb.UserStatusActive, events.UserReinstated
	if req.GetStatus() == userv1.UserStatus_USER_STATUS_SUSPENDED {
		status, eventType = genDb.UserStatusSuspended, events.UserSuspended
	}
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, setErr := qtx.SetUserStatus(ctx, genDb.SetUserStatusParams{ID: userID, Status: status}); setErr != nil {
			return setErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        eventType,
			SubjectType: events.SubjectUser,
			SubjectID:   new(userID),
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to set user status", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
		slog.ErrorContext(ctx, "failed to revoke token", "error", err)
		return nil, connect.NewError(connect.CodeInternal, publicRequestFailed).WithCause(err)
	}

	slog.InfoContext(ctx, "user logged out")
	return res, nil
}

// Helper methods

func (s *UserServer) getUserByID(ctx context.Context, id string) (*userv1.User, error) {
	user, err := s.queries.GetUserByID(ctx, uuid.MustParse(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	return dbUserToProto(user), nil
}

func dbUserToProto(user genDb.User) *userv1.User {
	return &userv1.User{
		Id:        user.ID.String(),
		Email:     user.Email,
		Name:      derefString(user.Name),
		AvatarUrl: derefString(user.AvatarUrl),
		CreatedAt: timeutil.ParsePostgresTimestamp(user.CreatedAt),
		UpdatedAt: timeutil.ParsePostgresTimestamp(user.UpdatedAt),
	}
}
