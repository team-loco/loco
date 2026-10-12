package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/team-loco/loco/api/events"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

const environmentTypeProduction = "production"

var (
	ErrEnvironmentNotFound      = errors.New("environment not found")
	ErrEnvironmentNameNotUnique = errors.New("environment name already exists in this workspace")
	ErrEnvironmentInUse         = errors.New("environment has deployments - cannot delete")
)

// EnvironmentServer implements the EnvironmentService gRPC server.
type EnvironmentServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	machine *tvm.VendingMachine
}

// NewEnvironmentServer creates a new EnvironmentServer instance.
func NewEnvironmentServer(db *pgxpool.Pool, queries genDb.Querier, machine *tvm.VendingMachine) *EnvironmentServer {
	return &EnvironmentServer{db: db, queries: queries, machine: machine, authz: authz.New(db, queries)}
}

// CreateEnvironment creates a new environment in a workspace.
func (s *EnvironmentServer) CreateEnvironment(
	ctx context.Context,
	req *environmentv1.CreateEnvironmentRequest,
) (*environmentv1.CreateEnvironmentResponse, error) {
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateEnvironment, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create environment", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized.Error()).WithCause(ErrUnauthorized)
	}

	workspaceID := uuid.MustParse(r.GetWorkspaceId())

	envType := protoEnvTypeToString(r.GetType())

	desc := r.GetDescription()
	var env genDb.Environment
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
			WorkspaceID:     workspaceID,
			Name:            r.GetName(),
			Description:     &desc,
			EnvironmentType: envType,
			CreatedBy:       entity.ID,
		})
		if isPgConstraintViolation(createErr) {
			return connect.NewError(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique.Error()).
				WithCause(ErrEnvironmentNameNotUnique)
		}
		if createErr != nil {
			return createErr
		}
		env = created
		return events.Record(ctx, qtx, events.Event{
			Type:        events.EnvironmentCreated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectEnvironment,
			SubjectID:   new(env.ID),
			Data:        map[string]any{events.FieldName: env.Name},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to create environment", err)
	}

	return &environmentv1.CreateEnvironmentResponse{
		EnvironmentId: env.ID.String(),
	}, nil
}

// GetEnvironment retrieves an environment by ID.
func (s *EnvironmentServer) GetEnvironment(
	ctx context.Context,
	req *environmentv1.GetEnvironmentRequest,
) (*environmentv1.GetEnvironmentResponse, error) {
	r := req

	envID := uuid.MustParse(r.GetEnvironmentId())

	env, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetEnvironment, env.WorkspaceID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	return &environmentv1.GetEnvironmentResponse{
		Environment: dbEnvToProto(env),
	}, nil
}

// ListEnvironments lists all environments in a workspace.
func (s *EnvironmentServer) ListEnvironments(
	ctx context.Context,
	req *environmentv1.ListEnvironmentsRequest,
) (*environmentv1.ListEnvironmentsResponse, error) {
	r := req

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListEnvironments, r.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list environments", "workspaceId", r.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	workspaceID := uuid.MustParse(r.GetWorkspaceId())

	envs, err := s.queries.ListWorkspaceEnvironments(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list environments", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}

	var protoEnvs []*environmentv1.Environment
	for _, env := range envs {
		protoEnvs = append(protoEnvs, dbEnvToProto(env))
	}

	return &environmentv1.ListEnvironmentsResponse{
		Environments: protoEnvs,
	}, nil
}

// UpdateEnvironment updates an environment.
func (s *EnvironmentServer) UpdateEnvironment(
	ctx context.Context,
	req *environmentv1.UpdateEnvironmentRequest,
) (*environmentv1.UpdateEnvironmentResponse, error) {
	r := req

	envID := uuid.MustParse(r.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateEnvironment, existing.WorkspaceID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to update environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	// Apply field mask: use existing values for unset fields.
	name := existing.Name
	if r.GetName() != "" {
		name = r.GetName()
	}
	description := existing.Description
	if r.GetDescription() != "" {
		d := r.GetDescription()
		description = &d
	}
	envType := existing.EnvironmentType
	if r.Type != nil {
		envType = protoEnvTypeToString(r.GetType())
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, existing.WorkspaceID); lockErr != nil {
			return lockErr
		}
		updated, updateErr := qtx.UpdateEnvironment(ctx, genDb.UpdateEnvironmentParams{
			ID:              envID,
			Name:            name,
			Description:     description,
			EnvironmentType: envType,
		})
		if isPgConstraintViolation(updateErr) {
			return connect.NewError(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique.Error()).
				WithCause(ErrEnvironmentNameNotUnique)
		}
		if updateErr != nil {
			return updateErr
		}
		if updated.Name != existing.Name || updated.EnvironmentType != existing.EnvironmentType {
			if bumpErr := bumpEnvironmentRevision(ctx, qtx, envID); bumpErr != nil {
				return bumpErr
			}
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.EnvironmentUpdated,
			WorkspaceID: new(existing.WorkspaceID),
			SubjectType: events.SubjectEnvironment,
			SubjectID:   new(envID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to update environment", err)
	}

	return &environmentv1.UpdateEnvironmentResponse{
		EnvironmentId: r.GetEnvironmentId(),
	}, nil
}

// DeleteEnvironment deletes an environment, refusing if any resources use it.
func (s *EnvironmentServer) DeleteEnvironment(
	ctx context.Context,
	req *environmentv1.DeleteEnvironmentRequest,
) (*environmentv1.DeleteEnvironmentResponse, error) {
	r := req

	envID := uuid.MustParse(r.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteEnvironment, existing.WorkspaceID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to delete environment", "environmentId", r.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	count, err := s.queries.CountDeploymentsByEnvironment(ctx, envID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count deployments for environment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	if count > 0 {
		slog.WarnContext(
			ctx,
			"cannot delete environment with deployments",
			"environmentId",
			r.GetEnvironmentId(),
			"count",
			count,
		)
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrEnvironmentInUse.Error()).
			WithCause(ErrEnvironmentInUse)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if deleteErr := qtx.DeleteEnvironment(ctx, envID); deleteErr != nil {
			return deleteErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.EnvironmentDeleted,
			WorkspaceID: new(existing.WorkspaceID),
			SubjectType: events.SubjectEnvironment,
			SubjectID:   new(envID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete environment", err)
	}

	return &environmentv1.DeleteEnvironmentResponse{}, nil
}

// dbEnvToProto converts a db.Environment to its proto representation.
func dbEnvToProto(env genDb.Environment) *environmentv1.Environment {
	return &environmentv1.Environment{
		Id:          env.ID.String(),
		WorkspaceId: env.WorkspaceID.String(),
		Name:        env.Name,
		Description: env.Description,
		Type:        stringToProtoEnvType(env.EnvironmentType),
		CreatedBy:   env.CreatedBy.String(),
		CreatedAt:   timeutil.ParsePostgresTimestamp(env.CreatedAt),
		UpdatedAt:   timeutil.ParsePostgresTimestamp(env.UpdatedAt),
		Revision:    env.Revision,
	}
}

func protoEnvTypeToString(t environmentv1.EnvironmentType) string {
	switch t {
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_DEV:
		return "dev"
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING:
		return "staging"
	case environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION:
		return environmentTypeProduction
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
	case environmentTypeProduction:
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
	default:
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED
	}
}
