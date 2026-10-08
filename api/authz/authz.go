package authz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	queries "github.com/team-loco/loco/api/gen/db"
)

var (
	ErrInsufficientPermissions = errors.New("insufficient permissions")
	ErrEntityNotFound          = errors.New("entity not found or invalid entity")
)

type Authorizer struct {
	pool    *pgxpool.Pool
	queries queries.Querier
}

func New(pool *pgxpool.Pool, q queries.Querier) *Authorizer {
	return &Authorizer{pool: pool, queries: q}
}

// Check reports whether granted covers required, either directly, through a system scope of the
// same level, or through the same scope on an organization or workspace that owns the entity.
// It returns [ErrEntityNotFound] when the entity does not exist and
// [ErrInsufficientPermissions] when no granted scope covers it.
func (a *Authorizer) Check(ctx context.Context, granted []queries.EntityScope, required queries.EntityScope) error {
	for _, scope := range granted {
		if scope == required {
			return nil
		}
		if scope.EntityType == queries.EntityTypeSystem && scope.Scope == required.Scope {
			return nil
		}
	}

	var implied []queries.EntityScope
	switch required.EntityType {
	case queries.EntityTypeOrganization:
		return ErrInsufficientPermissions
	case queries.EntityTypeUser:
		return ErrInsufficientPermissions
	case queries.EntityTypeWorkspace:
		orgID, err := a.queries.GetOrganizationIDByWorkspaceID(ctx, required.EntityID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to look up workspace owner", "error", err)
			return ErrEntityNotFound
		}
		implied = []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: orgID, Scope: required.Scope},
		}
	case queries.EntityTypeResource:
		ids, err := a.queries.GetWorkspaceOrganizationIDByResourceID(ctx, required.EntityID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to look up resource owner", "error", err)
			return ErrEntityNotFound
		}
		implied = []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: ids.OrgID, Scope: required.Scope},
			{EntityType: queries.EntityTypeWorkspace, EntityID: ids.WorkspaceID, Scope: required.Scope},
		}
	case queries.EntityTypeSystem:
		return ErrInsufficientPermissions
	default:
		return ErrEntityNotFound
	}

	for _, scope := range implied {
		if slices.Contains(granted, scope) {
			return nil
		}
	}
	return ErrInsufficientPermissions
}

func (a *Authorizer) UserScopes(ctx context.Context, userID uuid.UUID) ([]queries.EntityScope, error) {
	rows, err := a.queries.GetUserScopes(ctx, userID)
	if err != nil {
		return nil, err
	}
	scopes := make([]queries.EntityScope, len(rows))
	for i, row := range rows {
		scopes[i] = queries.EntityScope(row)
	}
	return scopes, nil
}

// UpdateRoles adds and removes scopes for a user in one transaction.
func (a *Authorizer) UpdateRoles(
	ctx context.Context,
	userID uuid.UUID,
	addScopes []queries.EntityScope,
	removeScopes []queries.EntityScope,
) error {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back role update", "error", rbErr)
		}
	}()
	if err := ApplyRoles(ctx, queries.New(tx), userID, addScopes, removeScopes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func ApplyRoles(
	ctx context.Context,
	q queries.Querier,
	userID uuid.UUID,
	addScopes []queries.EntityScope,
	removeScopes []queries.EntityScope,
) error {
	for _, es := range addScopes {
		if err := q.AddUserScope(ctx, queries.AddUserScopeParams{
			UserID:     userID,
			EntityType: es.EntityType,
			EntityID:   es.EntityID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("add user scope: %w", err)
		}
	}
	for _, es := range removeScopes {
		if err := q.RemoveUserScope(ctx, queries.RemoveUserScopeParams{
			UserID:     userID,
			EntityType: es.EntityType,
			EntityID:   es.EntityID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("remove user scope: %w", err)
		}
	}
	return nil
}
