package tvm

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	queries "github.com/team-loco/loco/api/gen/db"
)

// GetRoles returns all roles for the given user associated with the given token. The token must have user:read for the user the token is associated with.
func (tvm *VendingMachine) GetRoles(ctx context.Context, token string) ([]queries.EntityScope, error) {
	// get the token data
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("get user scopes: %w", err)
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	if tokenData.EntityType != queries.EntityTypeUser {
		return nil, ErrImproperUsage
	}

	// must have user:read on the token
	canRead := false
	for _, scope := range tokenData.Scopes {
		if scope.EntityType == queries.EntityTypeUser && scope.EntityID == tokenData.EntityID && scope.Scope == queries.ScopeRead {
			// token has user:read, proceed
			canRead = true
			break
		}
	}
	if !canRead {
		return nil, ErrInsufficentPermissions
	}

	// get the user's scopes to return to them
	userScopes, err := tvm.queries.GetUserScopes(ctx, tokenData.EntityID)
	if err != nil {
		return nil, fmt.Errorf("get user scopes: %w", err)
	}

	return userScopes, nil
}

// GetRolesByEntity returns all roles for the given entity and below for the given user. The token must have read on the given entity.
func (tvm *VendingMachine) GetRolesByEntity(ctx context.Context, token string, userID int64, entity queries.Entity) ([]queries.EntityScope, error) {
	// returns all roles for the given entity and below (so if entity is org, returns org, workspace, resource roles that are explicitly listed)

	// must have read on the entity
	if err := tvm.Verify(ctx, token, queries.EntityScope{
		EntityType: entity.Type,
		EntityID:   entity.ID,
		Scope:      queries.ScopeRead,
	}); err != nil {
		return nil, err
	}

	switch entity.Type {
	case queries.EntityTypeOrganization:
		// organization: get all org, workspace, resource roles
		rows, err := tvm.queries.GetUserScopesOnOrganization(ctx, queries.GetUserScopesOnOrganizationParams{
			UserID: userID,
			ID:     entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on organization: %w", err)
		}
		return rows, nil
	case queries.EntityTypeWorkspace:
		// workspace: get all workspace, resource roles
		rows, err := tvm.queries.GetUserScopesOnWorkspace(ctx, queries.GetUserScopesOnWorkspaceParams{
			UserID: userID,
			ID:     entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on workspace: %w", err)
		}
		return rows, nil
	case queries.EntityTypeResource, queries.EntityTypeUser, queries.EntityTypeSystem:
		// resource or user: only get roles on that entity
		userScopes, err := tvm.queries.GetUserScopesOnEntity(ctx, queries.GetUserScopesOnEntityParams{
			UserID:     userID,
			EntityType: entity.Type,
			EntityID:   entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on entity: %w", err)
		}
		return userScopes, nil
	}
	return nil, ErrEntityNotFound
}

// UpdateRoles updates the roles for the given user by adding and removing the given scopes.
func (tvm *VendingMachine) UpdateRoles(ctx context.Context, token string, userID int64, addScopes []queries.EntityScope, removeScopes []queries.EntityScope) error {
	// find each entity being added and removed
	// make sure the token has admin, explicitly or implicitly, on all of these entities by calling Verify
	// if ALL checks pass, update the scopes in the database
	// if any checks fail, return ErrInsufficentPermissions

	entities := []queries.Entity{}

	// collect unique entities that the token is requesting to add or remove scopes
	for _, es := range append(addScopes, removeScopes...) {
		// construct entity
		e := queries.Entity{
			Type: es.EntityType,
			ID:   es.EntityID,
		}
		if !slices.Contains(entities, e) { // avoid duplicates
			entities = append(entities, e)
		}
	}
	// verify that the token has admin on all of these entities
	for _, entity := range entities {
		err := tvm.Verify(ctx, token, queries.EntityScope{
			EntityType: entity.Type,
			EntityID:   entity.ID,
			Scope:      queries.ScopeAdmin,
		})
		if err != nil { // insufficient permissions or other error, bye bye no update 4 u
			return err
		}
	}

	// token has admin on all entities, proceed with update

	// use a transaction to ensure all or nothing
	tx, err := tvm.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := tvm.queries.(*queries.Queries).WithTx(tx)

	for _, es := range addScopes {
		if err := qtx.AddUserScope(ctx, queries.AddUserScopeParams{
			UserID:     userID,
			EntityType: es.EntityType,
			EntityID:   es.EntityID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("add user scope: %w", err)
		}
	}
	for _, es := range removeScopes {
		if err := qtx.RemoveUserScope(ctx, queries.RemoveUserScopeParams{
			UserID:     userID,
			EntityType: es.EntityType,
			EntityID:   es.EntityID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("remove user scope: %w", err)
		}
	}
	err = tx.Commit(ctx)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
