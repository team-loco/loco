package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	defaultEnvironmentName = "production"
)

var (
	ErrEnvironmentNotFound    = rpcerr.NewCoded(connect.CodeNotFound, "environment not found")
	errInvalidEnvironmentName = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"environment name must be 1 to 63 characters and not blank",
	)
	errInvalidEnvironmentSlug = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"environment slug must be 1 to 63 lowercase letters, digits and hyphens, "+
			"starting with a letter and ending with a letter or digit, and not localhost or resource-<UUID>",
	)
	ErrEnvironmentNameNotUnique = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"environment name already exists in this workspace",
	)
	ErrEnvironmentSlugNotUnique = rpcerr.NewCoded(
		connect.CodeAlreadyExists,
		"environment slug already exists in this workspace",
	)
	ErrEnvironmentInUse = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"environment has deployments - cannot delete",
	)
)

// EnvironmentServer implements the EnvironmentService gRPC server.
type EnvironmentServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	authz   *authz.Authorizer
	counts  *configv1.CountLimits
}

// NewEnvironmentServer creates a new EnvironmentServer instance.
func NewEnvironmentServer(db *pgxpool.Pool, queries genDb.Querier, counts *configv1.CountLimits) *EnvironmentServer {
	return &EnvironmentServer{db: db, queries: queries, authz: authz.New(queries), counts: counts}
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
	if err = check(ctx, s.authz, p, actions.New(actions.CreateEnvironment, workspaceID)); err != nil {
		return nil, err
	}

	name, ok := label(req.GetName())
	if !ok {
		return nil, errInvalidEnvironmentName
	}
	slug := req.GetSlug()
	if req.Slug == nil {
		slug = locofile.Slug(name)
	}
	if !validSlug(slug) {
		return nil, errInvalidEnvironmentSlug
	}
	desc := req.GetDescription()
	var env genDb.Environment
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if limitErr := checkWorkspaceLimit(
			ctx, qtx, s.counts, limits.EnvironmentsPerWorkspace, workspaceID, qtx.CountWorkspaceEnvironments,
		); limitErr != nil {
			return limitErr
		}
		// Waits for a write in progress to every environment, so the new one starts from its result.
		if lockErr := lockWorkspaceEnvironments(ctx, qtx, workspaceID); lockErr != nil {
			return lockErr
		}
		created, createErr := qtx.CreateEnvironment(ctx, genDb.CreateEnvironmentParams{
			WorkspaceID: workspaceID,
			Slug:        slug,
			Name:        name,
			Description: &desc,
			CreatedBy:   p.ActorID,
		})
		if keyErr := environmentKeyConflict(createErr, name, slug); keyErr != nil {
			return keyErr
		}
		if createErr != nil {
			return fmt.Errorf("insert environment: %w", createErr)
		}
		env = created
		return events.Record(ctx, qtx, events.Event{
			Type:          events.EnvironmentCreated,
			EnvironmentID: new(env.ID),
			WorkspaceID:   new(workspaceID),
			SubjectType:   events.SubjectEnvironment,
			SubjectID:     new(env.ID),
			Data:          map[string]any{events.FieldName: env.Name},
		})
	})
	if err != nil {
		return nil, err
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
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	readErr := check(ctx, s.authz, p, actions.New(actions.ListEnvironments, workspaceID))
	if readErr != nil {
		resourceIDs := grantedResourceIDs(p.Scopes)
		if connect.CodeOf(readErr) != connect.CodePermissionDenied || len(resourceIDs) == 0 {
			return nil, readErr
		}
		holds, holdsErr := s.queries.WorkspaceHoldsAnyResource(ctx, genDb.WorkspaceHoldsAnyResourceParams{
			WorkspaceID: workspaceID,
			ResourceIds: resourceIDs,
		})
		if holdsErr != nil {
			return nil, fmt.Errorf("check resource grants: %w", holdsErr)
		}
		if !holds {
			return nil, readErr
		}
	}

	envs, err := s.queries.ListWorkspaceEnvironments(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}

	var protoEnvs []*environmentv1.Environment
	for _, env := range envs {
		if readErr == nil {
			protoEnvs = append(protoEnvs, dbEnvToProto(env))
			continue
		}
		// A caller who reads only resources in this workspace needs the environments to scope
		// those resources' deployments and telemetry, not their details.
		protoEnvs = append(protoEnvs, &environmentv1.Environment{
			Id:          env.ID.String(),
			WorkspaceId: env.WorkspaceID.String(),
			Slug:        env.Slug,
			Name:        env.Name,
		})
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

	params := genDb.UpdateEnvironmentParams{ID: envID, Description: req.Description}
	if req.Name != nil {
		name, ok := label(req.GetName())
		if !ok {
			return nil, errInvalidEnvironmentName
		}
		params.Name = &name
	}

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		updated, updateErr := qtx.UpdateEnvironment(ctx, params)
		if isUniqueViolation(updateErr, pgEnvironmentNameUnique) {
			return ErrEnvironmentNameNotUnique.Detail("%s", derefString(params.Name))
		}
		if updateErr != nil {
			return fmt.Errorf("update environment: %w", updateErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.EnvironmentUpdated,
			EnvironmentID: new(envID),
			WorkspaceID:   new(existing.WorkspaceID),
			SubjectType:   events.SubjectEnvironment,
			SubjectID:     new(envID),
			Data:          map[string]any{events.FieldName: updated.Name},
		})
	})
	if err != nil {
		return nil, err
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
		_, lockErr := qtx.LockEnvironmentForDeletion(ctx, envID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrEnvironmentNotFound
		}
		if lockErr != nil {
			return fmt.Errorf("lock environment: %w", lockErr)
		}
		count, countErr := qtx.CountDeploymentsByEnvironment(ctx, envID)
		if countErr != nil {
			return fmt.Errorf("count deployments: %w", countErr)
		}
		if count > 0 {
			return ErrEnvironmentInUse
		}
		if deleteErr := qtx.DeleteEnvironment(ctx, envID); deleteErr != nil {
			return fmt.Errorf("delete environment: %w", deleteErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:          events.EnvironmentDeleted,
			EnvironmentID: new(envID),
			WorkspaceID:   new(existing.WorkspaceID),
			SubjectType:   events.SubjectEnvironment,
			SubjectID:     new(envID),
			Data:          map[string]any{events.FieldName: existing.Name},
		})
	})
	if err != nil {
		return nil, err
	}

	return &environmentv1.DeleteEnvironmentResponse{}, nil
}

// dbEnvToProto converts a db.Environment to its proto representation.
func dbEnvToProto(env genDb.Environment) *environmentv1.Environment {
	return &environmentv1.Environment{
		Id:          env.ID.String(),
		WorkspaceId: env.WorkspaceID.String(),
		Slug:        env.Slug,
		Name:        env.Name,
		Description: env.Description,
		CreatedBy:   env.CreatedBy.String(),
		CreatedAt:   timestamppb.New(env.CreatedAt),
		UpdatedAt:   timestamppb.New(env.UpdatedAt),
		Revision:    env.Revision,
	}
}

// environmentKeyConflict maps a write that hit another environment's name or slug to its client
// error.
func environmentKeyConflict(err error, name, slug string) error {
	switch {
	case isUniqueViolation(err, pgEnvironmentNameUnique):
		return ErrEnvironmentNameNotUnique.Detail("%s", name)
	case isUniqueViolation(err, pgEnvironmentSlugUnique):
		return ErrEnvironmentSlugNotUnique.Detail("%s", slug)
	}
	return nil
}
