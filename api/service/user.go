package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/loco-team/loco/api/gen/db"
	"github.com/loco-team/loco/api/timeutil"
	userv1 "github.com/loco-team/loco/shared/proto/user/v1"
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
	queries *genDb.Queries
}

// NewUserServer creates a new UserServer instance
func NewUserServer(db *pgxpool.Pool, queries *genDb.Queries) *UserServer {
	return &UserServer{db: db, queries: queries}
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

	if r.GetId() == 0 && r.GetEmail() == "" {
		slog.ErrorContext(ctx, "invalid request: either id or email must be provided")
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidRequest)
	}

	var user *userv1.User
	var err error

	if r.GetId() != 0 {
		user, err = s.getUserByID(ctx, r.GetId())
	} else {
		user, err = s.getUserByEmail(ctx, r.GetEmail())
	}

	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&userv1.GetUserResponse{
		User: user,
	}), nil
}

// GetCurrentUser retrieves the current user from JWT
func (s *UserServer) GetCurrentUser(
	ctx context.Context,
	req *connect.Request[userv1.GetCurrentUserRequest],
) (*connect.Response[userv1.GetCurrentUserResponse], error) {
	userID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	user, err := s.getUserByID(ctx, userID)
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

	currentUserID, ok := ctx.Value("userId").(int64)
	if !ok {
		slog.ErrorContext(ctx, "userId not found in context")
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	if r.Id != currentUserID {
		slog.WarnContext(ctx, "user attempted to update another user", "target_user", r.Id, "currentUser", currentUserID)
		return nil, connect.NewError(connect.CodePermissionDenied, ErrUnauthorized)
	}

	avatarURL := pgtype.Text{String: r.GetAvatarUrl(), Valid: r.GetAvatarUrl() != ""}

	user, err := s.queries.UpdateUserAvatarURL(ctx, genDb.UpdateUserAvatarURLParams{
		ID:        r.Id,
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

	page := r.Page
	if page < 1 {
		page = 1
	}

	perPage := r.PerPage
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 100 {
		perPage = 100
	}

	offset := (page - 1) * perPage

	totalCount, err := s.queries.CountUsers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count users", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	dbUsers, err := s.queries.ListUsers(ctx, genDb.ListUsersParams{
		Limit:  perPage,
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
		TotalCount: int32(totalCount),
		Page:       page,
		PerPage:    perPage,
	}), nil
}

// DeleteUser deletes a user (only if no active resources)
func (s *UserServer) DeleteUser(
	ctx context.Context,
	req *connect.Request[userv1.DeleteUserRequest],
) (*connect.Response[userv1.DeleteUserResponse], error) {
	r := req.Msg

	hasWorkspaces, err := s.queries.CheckUserHasWorkspaces(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user workspaces", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasWorkspaces {
		slog.WarnContext(ctx, "cannot delete user with active workspace memberships", "userId", r.Id)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasActiveResources)
	}

	hasOrganizations, err := s.queries.CheckUserHasOrganizations(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check user organizations", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	if hasOrganizations {
		slog.WarnContext(ctx, "cannot delete user with owned organizations", "userId", r.Id)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrUserHasOrganizations)
	}

	err = s.queries.DeleteUser(ctx, r.Id)
	if err != nil {
		slog.ErrorContext(ctx, "failed to delete user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&userv1.DeleteUserResponse{
		Success: true,
	}), nil
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

func (s *UserServer) getUserByEmail(ctx context.Context, email string) (*userv1.User, error) {
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		slog.ErrorContext(ctx, "failed to query user by email", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
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
