package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	userv1 "github.com/team-loco/loco/shared/proto/loco/user/v1"
)

var (
	ErrUserNotFound           = errors.New("user not found")
	ErrUserAlreadyExists      = errors.New("user already exists")
	ErrEmailAlreadyRegistered = errors.New("email already registered with different provider")
	ErrInvalidRequest         = errors.New("invalid request")
	ErrUserHasActiveResources = errors.New("user owns workspaces with active resources")
	ErrUserHasOrganizations   = errors.New("user owns organizations")
	ErrUnauthorized           = errors.New("unauthorized")
)

// UserServer implements the UserService gRPC server
type UserServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	tvm     *tvm.VendingMachine
}

// NewUserServer creates a new UserServer instance
func NewUserServer(db *pgxpool.Pool, queries genDb.Querier, tvm *tvm.VendingMachine) *UserServer {
	return &UserServer{db: db, queries: queries, tvm: tvm}
}

// CreateUser handles user creation with auto-org and workspace setup
func (s *UserServer) CreateUser(
	ctx context.Context,
	req *connect.Request[userv1.CreateUserRequest],
) (*connect.Response[userv1.CreateUserResponse], error) {
	r := req.Msg

	if r.GetExternalId() == "" || r.GetEmail() == "" {
		slog.ErrorContext(ctx, "invalid request: missing required fields")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	defer tx.Rollback(ctx)

	existingUserByEmail, err := s.queries.GetUserByEmail(ctx, r.GetEmail())
	if err == nil {
		if existingUserByEmail.ExternalID == r.GetExternalId() {
			if err := tx.Commit(ctx); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
			}
			return connect.NewResponse(&userv1.CreateUserResponse{UserId: existingUserByEmail.ID}), nil
		}

		slog.WarnContext(ctx, "email already registered with different provider", "email", r.GetEmail())
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrEmailAlreadyRegistered)
	}

	existingUserByExtID, err := s.queries.GetUserByExternalID(ctx, r.GetExternalId())
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		return connect.NewResponse(&userv1.CreateUserResponse{UserId: existingUserByExtID.ID}), nil
	}

	// Create new user
	avatarURL := pgtype.Text{String: r.GetAvatarUrl(), Valid: r.GetAvatarUrl() != ""}
	name := pgtype.Text{String: r.GetName(), Valid: r.GetName() != ""}

	user, err := s.queries.CreateUser(ctx, genDb.CreateUserParams{
		ExternalID: r.GetExternalId(),
		Email:      r.GetEmail(),
		Name:       name,
		AvatarUrl:  avatarURL,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if err := tx.Commit(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to commit transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	err = s.tvm.UpdateRoles(ctx, user.ID, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeUser, EntityID: user.ID, Scope: genDb.ScopeAdmin},
	}, []genDb.EntityScope{})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update user roles", "error", err, "userId", user.ID)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.CreateUserResponse{UserId: user.ID}), nil
}

// GetUser retrieves a user by ID or email
func (s *UserServer) GetUser(
	ctx context.Context,
	req *connect.Request[userv1.GetUserRequest],
) (*connect.Response[userv1.GetUserResponse], error) {
	r := req.Msg

	var targetUserID int64
	var err error

	switch key := r.GetKey().(type) {
	case *userv1.GetUserRequest_UserId:
		targetUserID = key.UserId
	case *userv1.GetUserRequest_Email:
		dbUser, err := s.queries.GetUserByEmail(ctx, key.Email)
		if err != nil {
			slog.ErrorContext(ctx, "failed to query user by email", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		targetUserID = dbUser.ID
	default:
		slog.ErrorContext(ctx, "invalid request: either id or email must be provided")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.GetUser, targetUserID)); err != nil {
		slog.WarnContext(ctx, "unauthorized to get user", "userId", targetUserID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	user, err := s.getUserByID(ctx, targetUserID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.GetUserResponse{User: user}), nil
}

// WhoAmI retrieves the current authenticated user
func (s *UserServer) WhoAmI(
	ctx context.Context,
	req *connect.Request[userv1.WhoAmIRequest],
) (*connect.Response[userv1.WhoAmIResponse], error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.GetCurrentUser, entity.ID))
	if err != nil {
		slog.ErrorContext(ctx, "failed to verify token", "error", err)
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	user, err := s.getUserByID(ctx, entity.ID)
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "returning user")

	return connect.NewResponse(&userv1.WhoAmIResponse{User: user}), nil
}

// UpdateUser updates user information
func (s *UserServer) UpdateUser(
	ctx context.Context,
	req *connect.Request[userv1.UpdateUserRequest],
) (*connect.Response[userv1.UpdateUserResponse], error) {
	r := req.Msg

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		slog.ErrorContext(ctx, "entity not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.UpdateUser, r.GetUserId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update user", "targetUserId", r.GetUserId(), "currentUserId", entity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	avatarURL := pgtype.Text{String: r.GetAvatarUrl(), Valid: r.GetAvatarUrl() != ""}

	_, err := s.queries.UpdateUserAvatarURL(ctx, genDb.UpdateUserAvatarURLParams{
		ID:        r.GetUserId(),
		AvatarUrl: avatarURL,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.UpdateUserResponse{UserId: r.GetUserId()}), nil
}

// ListUsers lists all users with pagination
func (s *UserServer) ListUsers(
	ctx context.Context,
	req *connect.Request[userv1.ListUsersRequest],
) (*connect.Response[userv1.ListUsersResponse], error) {
	r := req.Msg

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.ListUsers, 0)); err != nil {
		slog.WarnContext(ctx, "unauthorized to list users")
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	pageSize := normalizePageSize(r.GetPageSize())

	var pageToken pgtype.Text
	if r.GetPageToken() != "" {
		cursorID, err := decodeCursor(r.GetPageToken())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		pageToken = pgtype.Text{
			String: fmt.Sprintf("%d", cursorID),
			Valid:  true,
		}
	}

	dbUsers, err := s.queries.ListUsers(ctx, genDb.ListUsersParams{
		Limit:     pageSize,
		PageToken: pageToken,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var users []*userv1.User
	for _, dbUser := range dbUsers {
		users = append(users, dbUserToProto(dbUser))
	}

	var nextPageToken string
	if len(dbUsers) == int(pageSize) {
		nextPageToken = encodeCursor(dbUsers[len(dbUsers)-1].ID)
	}

	return connect.NewResponse(&userv1.ListUsersResponse{
		Users:         users,
		NextPageToken: nextPageToken,
	}), nil
}

// DeleteUser deletes a user (only if no active resources)
func (s *UserServer) DeleteUser(
	ctx context.Context,
	req *connect.Request[userv1.DeleteUserRequest],
) (*connect.Response[userv1.DeleteUserResponse], error) {
	r := req.Msg

	entityScopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.DeleteUser, r.GetUserId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete user", "userId", r.GetUserId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	_, err := s.queries.GetUserByID(ctx, r.GetUserId())
	if err != nil {
		slog.WarnContext(ctx, "user not found", "user_id", r.GetUserId())
		return nil, connect.NewError(connect.CodeNotFound, ErrUserNotFound)
	}

	hasWorkspaces, err := s.queries.CheckUserHasWorkspaces(ctx, r.GetUserId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasWorkspaces {
		slog.WarnContext(ctx, "cannot delete user with active workspace memberships", "userId", r.GetUserId())
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasActiveResources)
	}

	hasOrganizations, err := s.queries.CheckUserHasOrganizations(ctx, r.GetUserId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user organizations", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasOrganizations {
		slog.WarnContext(ctx, "cannot delete user with owned organizations", "userId", r.GetUserId())
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasOrganizations)
	}

	err = s.queries.DeleteUser(ctx, r.GetUserId())
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.DeleteUserResponse{}), nil
}

// Logout logs out the user by clearing the session cookie
func (s *UserServer) Logout(
	ctx context.Context,
	req *connect.Request[userv1.LogoutRequest],
) (*connect.Response[userv1.LogoutResponse], error) {
	res := connect.NewResponse(&userv1.LogoutResponse{})
	res.Header().Set("Set-Cookie", "loco_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax")

	token, ok := ctx.Value(contextkeys.TokenKey).(string)
	if !ok {
		slog.WarnContext(ctx, "token not found in context")
		return res, nil
	}
	s.tvm.Revoke(ctx, token)

	slog.InfoContext(ctx, "user logged out")
	return res, nil
}

// Helper methods

func (s *UserServer) getUserByID(ctx context.Context, id int64) (*userv1.User, error) {
	user, err := s.queries.GetUserByID(ctx, id)
	if err != nil {
		slog.WarnContext(ctx, "user not found", "id", id)
		return nil, connect.NewError(connect.CodeNotFound, ErrUserNotFound)
	}

	return dbUserToProto(user), nil
}

func dbUserToProto(user genDb.User) *userv1.User {
	return &userv1.User{
		Id:         user.ID,
		ExternalId: user.ExternalID,
		Email:      user.Email,
		Name:       user.Name.String,
		AvatarUrl:  user.AvatarUrl.String,
		CreatedAt:  timeutil.ParsePostgresTimestamp(user.CreatedAt.Time),
		UpdatedAt:  timeutil.ParsePostgresTimestamp(user.UpdatedAt.Time),
	}
}
