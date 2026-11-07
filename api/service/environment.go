package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	"github.com/team-loco/loco/api/tvm"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	environmentTypeProduction = "production"
	updateNameField           = "name"
	updateDescriptionField    = "description"
	updateTypeField           = "type"
)

var (
	ErrEnvironmentNotFound      = errors.New("environment not found")
	ErrEnvironmentNameRequired  = errors.New("environment name must not be blank")
	ErrEnvironmentTypeRequired  = errors.New("environment type is required")
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
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err = check(ctx, s.authz, p, actions.New(actions.CreateEnvironment, workspaceID.String())); err != nil {
		return nil, err
	}

	envType := protoEnvTypeToString(req.GetType())

	desc := req.GetDescription()
	var env genDb.Environment
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		created, createErr := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
			WorkspaceID:     workspaceID,
			Name:            req.GetName(),
			Description:     &desc,
			EnvironmentType: envType,
			CreatedBy:       p.Entity.ID,
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

	env, err := visibleEnvironment(ctx, s.queries, s.authz, envID, actions.GetEnvironment)
	if err != nil {
		return nil, err
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
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.ListEnvironments, workspaceID); err != nil {
		return nil, err
	}

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

	existing, err := visibleEnvironment(ctx, s.queries, s.authz, envID, actions.UpdateEnvironment)
	if err != nil {
		return nil, err
	}

	message := req.ProtoReflect()
	fields := message.Descriptor().Fields()
	updateName := message.Has(fields.ByName(updateNameField))
	updateDescription := message.Has(fields.ByName(updateDescriptionField))
	updateType := message.Has(fields.ByName(updateTypeField))
	if updateName && strings.TrimSpace(req.GetName()) == "" {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrEnvironmentNameRequired)
	}
	if updateType &&
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
		if updateName {
			name = req.GetName()
		}
		description := current.Description
		if updateDescription {
			description = new(req.GetDescription())
		}
		envType := current.EnvironmentType
		if updateType {
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

// DeleteEnvironment deletes an environment, refusing while it has deployments.
func (s *EnvironmentServer) DeleteEnvironment(
	ctx context.Context,
	req *environmentv1.DeleteEnvironmentRequest,
) (*environmentv1.DeleteEnvironmentResponse, error) {
	envID := uuid.MustParse(req.GetEnvironmentId())

	existing, err := visibleEnvironment(ctx, s.queries, s.authz, envID, actions.DeleteEnvironment)
	if err != nil {
		return nil, err
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := lockWorkspaceEnvironments(ctx, qtx, existing.WorkspaceID); lockErr != nil {
			return lockErr
		}
		count, countErr := qtx.CountDeploymentsByEnvironment(ctx, envID)
		if countErr != nil {
			return fmt.Errorf("count deployments: %w", countErr)
		}
		if count > 0 {
			return rpcerr.New(connect.CodeFailedPrecondition, ErrEnvironmentInUse)
		}
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
	case "staging":
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING
	case environmentTypeProduction:
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION
	default:
		return environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED
	}
}
