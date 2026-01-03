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
	userv1 "github.com/team-loco/loco/shared/proto/user/v1"
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

	if r.ExternalId == "" || r.Email == "" {
		slog.ErrorContext(ctx, "invalid request: missing required fields")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	defer tx.Rollback(ctx)

	existingUserByEmail, err := s.queries.GetUserByEmail(ctx, r.Email)
	if err == nil {
		if existingUserByEmail.ExternalID == r.ExternalId {
			if err := tx.Commit(ctx); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
			}
			return connect.NewResponse(&userv1.CreateUserResponse{
				User: dbUserToProto(existingUserByEmail),
			}), nil
		}

		slog.WarnContext(ctx, "email already registered with different provider", "email", r.Email)
		return nil, connect.NewError(connect.CodeAlreadyExists, ErrEmailAlreadyRegistered)
	}

	existingUserByExtID, err := s.queries.GetUserByExternalID(ctx, r.ExternalId)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		return connect.NewResponse(&userv1.CreateUserResponse{
			User: dbUserToProto(existingUserByExtID),
		}), nil
	}

	// Create new user
	// TODO: Check GitHub collaborator status - for now default to false
	// This will be implemented when we add GitHub API client

	avatarURL := pgtype.Text{String: r.GetAvatarUrl(), Valid: r.GetAvatarUrl() != ""}
	name := pgtype.Text{String: r.GetName(), Valid: r.GetName() != ""}

	user, err := s.queries.CreateUser(ctx, genDb.CreateUserParams{
		ExternalID: r.ExternalId,
		Email:      r.Email,
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

	return connect.NewResponse(&userv1.CreateUserResponse{
		User: dbUserToProto(user),
	}), nil
}

// GetUser retrieves a user by ID or email
func (s *UserServer) GetUser(
	ctx context.Context,
	req *connect.Request[userv1.GetUserRequest],
) (*connect.Response[userv1.GetUserResponse], error) {
	r := req.Msg

	if r.GetUserId() == 0 && r.GetEmail() == "" {
		slog.ErrorContext(ctx, "invalid request: either id or email must be provided")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	var targetUserID int64
	if r.GetUserId() != 0 {
		targetUserID = r.GetUserId()
	} else {
		dbUser, err := s.queries.GetUserByEmail(ctx, r.GetEmail())
		if err != nil {
			slog.ErrorContext(ctx, "failed to query user by email", "error", err)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
		}
		targetUserID = dbUser.ID
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

	return connect.NewResponse(&userv1.GetUserResponse{
		User: user,
	}), nil
}

// GetCurrentUser retrieves the current user from loco token
func (s *UserServer) GetCurrentUser(
	ctx context.Context,
	req *connect.Request[userv1.GetCurrentUserRequest],
) (*connect.Response[userv1.GetCurrentUserResponse], error) {
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
	resp := connect.NewResponse(&userv1.GetCurrentUserResponse{User: user})

	return resp, nil
}

// UpdateUser updates user avatar URL
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

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.UpdateUser, r.UserId)); err != nil {
		slog.WarnContext(ctx, "unauthorized to update user", "targetUserId", r.UserId, "currentUserId", entity.ID)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	avatarURL := pgtype.Text{String: r.GetAvatarUrl(), Valid: r.GetAvatarUrl() != ""}

	user, err := s.queries.UpdateUserAvatarURL(ctx, genDb.UpdateUserAvatarURLParams{
		ID:        r.UserId,
		AvatarUrl: avatarURL,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.UpdateUserResponse{
		User: dbUserToProto(user),
	}), nil
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

	limit := r.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := r.Offset
	if offset < 0 {
		offset = 0
	}

	totalCount, err := s.queries.CountUsers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	dbUsers, err := s.queries.ListUsers(ctx, genDb.ListUsersParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var users []*userv1.User
	for _, dbUser := range dbUsers {
		users = append(users, dbUserToProto(dbUser))
	}

	return connect.NewResponse(&userv1.ListUsersResponse{
		Users:      users,
		TotalCount: int64(totalCount),
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

	if err := s.tvm.VerifyWithGivenEntityScopes(ctx, entityScopes, actions.New(actions.DeleteUser, r.UserId)); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete user", "userId", r.UserId)
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	user, err := s.queries.GetUserByID(ctx, r.UserId)
	if err != nil {
		slog.WarnContext(ctx, "user not found", "user_id", r.UserId)
		return nil, connect.NewError(connect.CodeNotFound, ErrUserNotFound)
	}

	hasWorkspaces, err := s.queries.CheckUserHasWorkspaces(ctx, r.UserId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasWorkspaces {
		slog.WarnContext(ctx, "cannot delete user with active workspace memberships", "userId", r.UserId)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasActiveResources)
	}

	hasOrganizations, err := s.queries.CheckUserHasOrganizations(ctx, r.UserId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user organizations", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasOrganizations {
		slog.WarnContext(ctx, "cannot delete user with owned organizations", "userId", r.UserId)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasOrganizations)
	}

	err = s.queries.DeleteUser(ctx, r.UserId)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.DeleteUserResponse{
		User:    dbUserToProto(user),
		Message: "User deleted successfully",
	}), nil
}

// Logout logs out the user by clearing the session cookie
func (s *UserServer) Logout(
	ctx context.Context,
	req *connect.Request[userv1.LogoutRequest],
) (*connect.Response[userv1.LogoutResponse], error) {
	res := connect.NewResponse(&userv1.LogoutResponse{
		Message: "logged out successfully",
	})

	res.Header().Set("Set-Cookie", "loco_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax")

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
