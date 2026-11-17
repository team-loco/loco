package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	queries "github.com/team-loco/loco/api/gen/db"
)

var (
	ErrInsufficientPermissions = errors.New("insufficient permissions")
	ErrEntityNotFound          = errors.New("entity not found or invalid entity")
)

type Authorizer struct {
	queries queries.Querier
}

func New(q queries.Querier) *Authorizer {
	return &Authorizer{queries: q}
}

// Check reports whether granted covers required, either directly, through a system scope of the
// same level, or through the same scope on an organization or workspace that owns the entity.
// It returns [ErrEntityNotFound] when the entity does not exist,
// [ErrInsufficientPermissions] when no granted scope covers it, and the wrapped database error
// when the owner lookup fails.
func (a *Authorizer) Check(ctx context.Context, granted []queries.EntityScope, required ...queries.EntityScope) error {
	parents := make(map[queries.Entity][]queries.EntityScope)
	for _, scope := range required {
		if err := a.check(ctx, granted, scope, parents); err != nil {
			return err
		}
	}
	return nil
}

// Intersect returns the scopes shared by both grants, including narrower descendant grants.
func (a *Authorizer) Intersect(ctx context.Context, left, right []queries.EntityScope) ([]queries.EntityScope, error) {
	var common []queries.EntityScope
	parents := make(map[queries.Entity][]queries.EntityScope)
	for _, l := range left {
		for _, r := range right {
			for _, pair := range [][2]queries.EntityScope{{l, r}, {r, l}} {
				err := a.check(ctx, []queries.EntityScope{pair[0]}, pair[1], parents)
				if errors.Is(err, ErrInsufficientPermissions) || errors.Is(err, ErrEntityNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				if !slices.Contains(common, pair[1]) {
					common = append(common, pair[1])
				}
			}
		}
	}
	return common, nil
}

func (a *Authorizer) check(
	ctx context.Context,
	granted []queries.EntityScope,
	required queries.EntityScope,
	parents map[queries.Entity][]queries.EntityScope,
) error {
	for _, scope := range granted {
		if !covers(scope.Scope, required.Scope) {
			continue
		}
		if scope.EntityType == required.EntityType && scope.EntityID == required.EntityID {
			return nil
		}
		if scope.EntityType == queries.EntityTypeSystem {
			return nil
		}
	}

	entity := queries.Entity{Type: required.EntityType, ID: required.EntityID}
	implied, ok := parents[entity]
	if !ok {
		switch required.EntityType {
		case queries.EntityTypeOrganization:
			return ErrInsufficientPermissions
		case queries.EntityTypeUser:
			return ErrInsufficientPermissions
		case queries.EntityTypeWorkspace:
			orgID, err := a.queries.GetOrganizationIDByWorkspaceID(ctx, required.EntityID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrEntityNotFound
			}
			if err != nil {
				return fmt.Errorf("look up workspace owner: %w", err)
			}
			implied = []queries.EntityScope{
				{EntityType: queries.EntityTypeOrganization, EntityID: orgID},
			}
		case queries.EntityTypeResource:
			ids, err := a.queries.GetWorkspaceOrganizationIDByResourceID(ctx, required.EntityID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrEntityNotFound
			}
			if err != nil {
				return fmt.Errorf("look up resource owner: %w", err)
			}
			implied = []queries.EntityScope{
				{EntityType: queries.EntityTypeOrganization, EntityID: ids.OrgID},
				{EntityType: queries.EntityTypeWorkspace, EntityID: ids.WorkspaceID},
			}
		case queries.EntityTypeSystem:
			return ErrInsufficientPermissions
		default:
			return ErrEntityNotFound
		}

		parents[entity] = implied
	}

	for _, scope := range implied {
		scope.Scope = required.Scope
		if slices.Contains(granted, scope) {
			return nil
		}
	}
	return ErrInsufficientPermissions
}

type ReadableEntities struct {
	System        bool
	Organizations []uuid.UUID
	Workspaces    []uuid.UUID
	Resources     []uuid.UUID
}

func (*Authorizer) ReadableEntities(granted []queries.EntityScope) ReadableEntities {
	var system bool
	var organizations, workspaces, resources []uuid.UUID
	for _, scope := range granted {
		if !covers(scope.Scope, queries.ScopeRead) {
			continue
		}
		switch scope.EntityType {
		case queries.EntityTypeSystem:
			system = true
		case queries.EntityTypeOrganization:
			organizations = append(organizations, scope.EntityID)
		case queries.EntityTypeWorkspace:
			workspaces = append(workspaces, scope.EntityID)
		case queries.EntityTypeResource:
			resources = append(resources, scope.EntityID)
		}
	}
	return ReadableEntities{System: system, Organizations: organizations, Workspaces: workspaces, Resources: resources}
}

func (a *Authorizer) ReadableWorkspaces(
	ctx context.Context,
	granted []queries.EntityScope,
	orgID uuid.UUID,
) (bool, []uuid.UUID) {
	orgRead := queries.EntityScope{
		EntityType: queries.EntityTypeOrganization,
		EntityID:   orgID,
		Scope:      queries.ScopeRead,
	}
	if a.Check(ctx, granted, orgRead) == nil {
		return true, nil
	}
	return false, a.ReadableEntities(granted).Workspaces
}

func covers(held queries.Scope, required queries.Scope) bool {
	return held == required || required == queries.ScopeMember
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
