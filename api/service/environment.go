package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const environmentTypeProduction = "production"

var (
	ErrEnvironmentNotFound      = errors.New("environment not found")
	ErrEnvironmentNameNotUnique = errors.New("environment name already exists in this workspace")
	ErrEnvironmentNameRequired  = errors.New("environment name must not be blank")
	ErrEnvironmentTypeRequired  = errors.New("environment type is required")
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
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.CreateEnvironment, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to create environment", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}

	workspaceID := uuid.MustParse(req.GetWorkspaceId())

	envType := protoEnvTypeToString(req.GetType())

	desc := req.GetDescription()
	var env genDb.Environment
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
			WorkspaceID:     workspaceID,
			Name:            req.GetName(),
			Description:     &desc,
			EnvironmentType: envType,
			CreatedBy:       entity.ID,
		})
		if isPgConstraintViolation(createErr) {
			return rpcerr.New(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique)
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
	envID := uuid.MustParse(req.GetEnvironmentId())

	env, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", req.GetEnvironmentId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.GetEnvironment, env.WorkspaceID.String()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to get environment", "environmentId", req.GetEnvironmentId())
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
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.ListEnvironments, req.GetWorkspaceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to list environments", "workspaceId", req.GetWorkspaceId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}

	workspaceID := uuid.MustParse(req.GetWorkspaceId())

	envs, err := s.queries.ListWorkspaceEnvironments(ctx, workspaceID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list environments", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
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
	envID := uuid.MustParse(req.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", req.GetEnvironmentId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.UpdateEnvironment, existing.WorkspaceID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to update environment", "environmentId", req.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	provided := []string{}
	if req.GetName() != "" {
		provided = append(provided, updateNameField)
	}
	if req.GetDescription() != "" {
		provided = append(provided, updateDescriptionField)
	}
	if req.GetType() != environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED {
		provided = append(provided, updateTypeField)
	}
	paths, maskErr := updateMaskPaths(
		req.GetUpdateMask(),
		[]string{updateNameField, updateDescriptionField, updateTypeField},
		provided,
	)
	if maskErr != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, maskErr)
	}
	if slices.Contains(paths, updateNameField) && strings.TrimSpace(req.GetName()) == "" {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrEnvironmentNameRequired)
	}
	if slices.Contains(paths, updateTypeField) &&
		req.GetType() == environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrEnvironmentTypeRequired)
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, existing.WorkspaceID); lockErr != nil {
			return lockErr
		}
		current, loadErr := qtx.GetEnvironmentByID(ctx, envID)
		if loadErr != nil {
			return loadErr
		}
		name := current.Name
		if slices.Contains(paths, updateNameField) {
			name = req.GetName()
		}
		description := current.Description
		if slices.Contains(paths, updateDescriptionField) {
			description = new(req.GetDescription())
		}
		envType := current.EnvironmentType
		if slices.Contains(paths, updateTypeField) {
			envType = protoEnvTypeToString(req.GetType())
		}
		updated, updateErr := qtx.UpdateEnvironment(ctx, genDb.UpdateEnvironmentParams{
			ID:              envID,
			Name:            name,
			Description:     description,
			EnvironmentType: envType,
		})
		if isPgConstraintViolation(updateErr) {
			return rpcerr.New(connect.CodeAlreadyExists, ErrEnvironmentNameNotUnique)
		}
		if updateErr != nil {
			return updateErr
		}
		if updated.Name != current.Name || updated.EnvironmentType != current.EnvironmentType {
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
		EnvironmentId: req.GetEnvironmentId(),
	}, nil
}

// DeleteEnvironment deletes an environment, refusing if any resources use it.
func (s *EnvironmentServer) DeleteEnvironment(
	ctx context.Context,
	req *environmentv1.DeleteEnvironmentRequest,
) (*environmentv1.DeleteEnvironmentResponse, error) {
	envID := uuid.MustParse(req.GetEnvironmentId())

	existing, err := s.queries.GetEnvironmentByID(ctx, envID)
	if err != nil {
		slog.WarnContext(ctx, "environment not found", "id", req.GetEnvironmentId())
		return nil, rpcerr.New(connect.CodeNotFound, ErrEnvironmentNotFound)
	}

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}

	if verifyErr := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.DeleteEnvironment, existing.WorkspaceID.String()),
	); verifyErr != nil {
		slog.WarnContext(ctx, "unauthorized to delete environment", "environmentId", req.GetEnvironmentId())
		return nil, connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(verifyErr)
	}

	count, err := s.queries.CountDeploymentsByEnvironment(ctx, envID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to count deployments for environment", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if count > 0 {
		slog.WarnContext(
			ctx,
			"cannot delete environment with deployments",
			"environmentId",
			req.GetEnvironmentId(),
			"count",
			count,
		)
		return nil, rpcerr.New(connect.CodeFailedPrecondition, ErrEnvironmentInUse)
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
