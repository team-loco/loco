package tvm

import (
	"context"
	"slices"
	"time"

	queries "github.com/team-loco/loco/api/gen/db"
)

// Verify verifies that the givenEntityScopes has the entityScope required, either explicitly or implicitly. It returns an error if an error
// occurs, if the entity does not exist [ErrEntityNotFound], or if the token does not have sufficient permissions [ErrInsufficentPermissions].
func (tvm *VendingMachine) VerifyWithGivenEntityScopes(ctx context.Context, givenEntityScopes []queries.EntityScope, entityScope queries.EntityScope) error {
	// hot path: check if token has the entityScope required or has sys:scope
	for _, scope := range givenEntityScopes {
		if scope == entityScope { // the token directly has the scope needed
			return nil
		}
		// for example: if operation requires workspace:write and user has sys:write
		// it should allow the operation. this function still does not allow access
		// for someone with something like sys:read to workspace:write.
		if scope.EntityType == queries.EntityTypeSystem && scope.Scope == entityScope.Scope {
			return nil
		}
	}

	// not so hot path: if the token has an entityScope that is *implied*
	var otherEntityScopes []queries.EntityScope
	switch entityScope.EntityType {
	case queries.EntityTypeOrganization, queries.EntityTypeUser:
		return ErrInsufficentPermissions // there is nothing higher to check.
	case queries.EntityTypeWorkspace:
		// lookup the org id for the workspace
		org_id, err := tvm.queries.GetOrganizationIDByWorkspaceID(ctx, entityScope.EntityID)
		if err != nil {
			// note: this could be another error
			return ErrEntityNotFound
		}

		// check for org:scope
		otherEntityScopes = []queries.EntityScope{
			{
				EntityType: queries.EntityTypeOrganization,
				EntityID:   org_id,
				Scope:      entityScope.Scope,
			},
		}
	case queries.EntityTypeResource:
		// lookup the workspace and org id for the resource
		ids, err := tvm.queries.GetWorkspaceOrganizationIDByResourceID(ctx, entityScope.EntityID)
		if err != nil {
			// note: again this could be another eror
			return ErrEntityNotFound
		}
		wks_id := ids.WorkspaceID
		org_id := ids.OrgID

		// check for org:scope and workspace:scope
		otherEntityScopes = []queries.EntityScope{
			{
				EntityType: queries.EntityTypeOrganization,
				EntityID:   org_id,
				Scope:      entityScope.Scope,
			},
			{
				EntityType: queries.EntityTypeWorkspace,
				EntityID:   wks_id,
				Scope:      entityScope.Scope,
			},
		}
	default:
		return ErrEntityNotFound // unknown entity type
	}

	// check otherentityscopes. note: someone see if this can be optimized
	for _, oes := range otherEntityScopes {
		// if token has any of the implied scopes, allow
		if slices.Contains(givenEntityScopes, oes) {
			return nil
		}
	}

	return ErrInsufficentPermissions
}

// Verify verifies that the given token has the entityScope required, either explicitly or implicitly. It returns an error if an error
// occurs, if the entity does not exist [ErrEntityNotFound], or if the token does not have sufficient permissions [ErrInsufficentPermissions].
// It also returns the entity associated with the token, for example, log in tokens will return the user entity, and tokens issues for an
// Resource will return the resource entity.
func (tvm *VendingMachine) VerifyWithEntity(ctx context.Context, token string, entityScope queries.EntityScope) (queries.Entity, error) {
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		return queries.Entity{}, ErrTokenNotFound
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return queries.Entity{}, ErrTokenExpired
	}
	tokenEntity := queries.Entity{
		Type: tokenData.EntityType,
		ID:   tokenData.EntityID,
	}

	return tokenEntity, tvm.VerifyWithGivenEntityScopes(ctx, tokenData.Scopes, entityScope)
}

// Verify verifies that the given token has the entityScope required, either explicitly or implicitly. It returns an error if an error
// occurs, if the entity does not exist [ErrEntityNotFound], or if the token does not have sufficient permissions [ErrInsufficentPermissions].
func (tvm *VendingMachine) Verify(ctx context.Context, token string, entityScope queries.EntityScope) error {
	_, err := tvm.VerifyWithEntity(ctx, token, entityScope)
	return err
}
