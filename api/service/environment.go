package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/actions"
	environmentv1 "github.com/team-loco/loco/proto/loco/environment/v1"
)

var (
	ErrEnvironmentNotFound      = errors.New("environment not found")
	ErrEnvironmentNameNotUnique = errors.New("environment name already exists in this workspace")
	ErrEnvironmentInUse         = errors.New("environment has deployments - cannot delete")
)

// EnvironmentServer implements the EnvironmentService gRPC server.
type EnvironmentServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
}

// NewEnvironmentServer creates a new EnvironmentServer instance.
func NewEnvironmentServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *EnvironmentServer {
	return &EnvironmentServer{db: db, queries: queries, machine: machine}
}

// CreateEnvironment creates a new environment in a workspace.
func (s *EnvironmentServer) CreateEnvironment(
	ctx context.Context,
	req *connect.Request[environmentv1.CreateEnvironmentRequest],
) (*connect.Response[environmentv1.CreateEnvironmentResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.CreateEnvironment, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to create environment", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID := uuid.MustParse(r.GetWorkspaceId())

	envType := protoEnvTypeToString(r.GetType())

	description := pgtype.Text{String: r.GetDescription(), Valid: r.GetDescription() != ""}

	env, err := s.queries.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
		WorkspaceID:     workspaceID,
		Name:            r.GetName(),
		Description:     description,
		EnvironmentType: envType,
		CreatedBy:       entity.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to create environment", "error", err)
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&environmentv1.CreateEnvironmentResponse{
		EnvironmentId: env.ID.String(),
	}), nil
}

// GetEnvironment retrieves an environment by ID.
func (s *EnvironmentServer) GetEnvironment(
	ctx context.Context,
	req *connect.Request[environmentv1.GetEnvironmentRequest],
) (*connect.Response[environmentv1.GetEnvironmentResponse], error) {
	r := req.Msg

	envID := uuid.MustParse(r.GetEnvironmentId())

	env, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.GetEnvironment, env.WorkspaceID.String())); err != nil {
		slog.WarnContext(ctx, "unauthorized to get environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	return connect.NewResponse(&environmentv1.GetEnvironmentResponse{
		Environment: dbEnvToProto(env),
	}), nil
}

// ListEnvironments lists all environments in a workspace.
func (s *EnvironmentServer) ListEnvironments(
	ctx context.Context,
	req *connect.Request[environmentv1.ListEnvironmentsRequest],
) (*connect.Response[environmentv1.ListEnvironmentsResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.ListEnvironments, r.GetWorkspaceId())); err != nil {
		slog.WarnContext(ctx, "unauthorized to list environments", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	workspaceID := uuid.MustParse(r.GetWorkspaceId())

	envs, err := s.queries.ListWorkspaceEnvironments(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list environments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	var protoEnvs []*environmentv1.Environment
	for _, env := range envs {
		protoEnvs = append(protoEnvs, dbEnvToProto(env))
	}

	return connect.NewResponse(&environmentv1.ListEnvironmentsResponse{
		Environments: protoEnvs,
	}), nil
}

// UpdateEnvironment updates an environment.
func (s *EnvironmentServer) UpdateEnvironment(
	ctx context.Context,
	req *connect.Request[environmentv1.UpdateEnvironmentRequest],
) (*connect.Response[environmentv1.UpdateEnvironmentResponse], error) {
	r := req.Msg

	envID := uuid.MustParse(r.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.UpdateEnvironment, existing.WorkspaceID.String())); err != nil {
		slog.WarnContext(ctx, "unauthorized to update environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	// Apply field mask: use existing values for unset fields.
	name := existing.Name
	if r.GetName() != "" {
		name = r.GetName()
	}
	description := existing.Description
	if r.GetDescription() != "" {
		description = pgtype.Text{String: r.GetDescription(), Valid: true}
	}
	envType := existing.EnvironmentType
	if r.Type != nil {
		envType = protoEnvTypeToString(r.GetType())
	}

	_, err = s.queries.UpdateEnvironment(ctx, genDb.UpdateEnvironmentParams{
		ID:              envID,
		Name:            name,
		Description:     description,
		EnvironmentType: envType,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to update environment", "error", err)
		if isPgConstraintViolation(err) {
			return nil, connect.NewError(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&environmentv1.UpdateEnvironmentResponse{
		EnvironmentId: r.GetEnvironmentId(),
	}), nil
}

// DeleteEnvironment deletes an environment, refusing if any resources use it.
func (s *EnvironmentServer) DeleteEnvironment(
	ctx context.Context,
	req *connect.Request[environmentv1.DeleteEnvironmentRequest],
) (*connect.Response[environmentv1.DeleteEnvironmentResponse], error) {
	r := req.Msg

	envID := uuid.MustParse(r.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("entity scopes not found in context"))
	}

	if err := s.machine.VerifyWithGivenEntityScopes(ctx, scopes, actions.New(actions.DeleteEnvironment, existing.WorkspaceID.String())); err != nil {
		slog.WarnContext(ctx, "unauthorized to delete environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	count, err := s.queries.CountDeploymentsByEnvironment(ctx, envID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count deployments for environment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}
	if count > 0 {
		slog.WarnContext(ctx, "cannot delete environment with deployments", "environmentId", r.GetEnvironmentId(), "count", count)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrEnvironmentInUse)
	}

	if err := s.queries.DeleteEnvironment(ctx, envID); err != nil {
		slog.ErrorContext(ctx, "failed to delete environment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("database error: %w", err))
	}

	return connect.NewResponse(&environmentv1.DeleteEnvironmentResponse{}), nil
}

// dbEnvToProto converts a db.Environment to its proto representation.
func dbEnvToProto(env genDb.Environment) *environmentv1.Environment {
	desc := env.Description.String
	return &environmentv1.Environment{
		Id:          env.ID.String(),
		WorkspaceId: env.WorkspaceID.String(),
		Name:        env.Name,
		Description: &desc,
		Type:        stringToProtoEnvType(env.EnvironmentType),
		CreatedBy:   env.CreatedBy.String(),
		CreatedAt:   timeutil.ParsePostgresTimestamp(env.CreatedAt.Time),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(env.UpdatedAt.Time),
	}
}

func protoEnvTypeToString(t environmentv1.EnvironmentType) string {
	switch t {
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_DEV:
		return "dev"
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING:
		return "staging"
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION:
		return "production"
	default:
		return ""
	}
}

func stringToProtoEnvType(s string) environmentv1.EnvironmentType {
	switch s {
	case "dev":
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_DEV
	case "staging":
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING
	case "production":
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
	default:
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED
	}
}
