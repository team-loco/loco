package tvm

import (
	"context"
	"log/slog"
	"slices"

	"github.com/team-loco/loco/api/contextkeys"
	queries "github.com/team-loco/loco/api/gen/db"
)

// VerifyWithGivenEntityScopes verifies that the givenEntityScopes has the entityScope required,
// either explicitly or implicitly. It returns an error if an error occurs, if the entity does
// not exist [ErrEntityNotFound], or if the token does not have sufficient permissions
// [ErrInsufficentPermissions].
func (tvm *VendingMachine) VerifyWithGivenEntityScopes(
	ctx context.Context,
	givenEntityScopes []queries.EntityScope,
	entityScope queries.EntityScope,
) error {
	if restriction, ok := ctx.Value(contextkeys.StackRestrictionKey).(*contextkeys.StackRestriction); ok &&
		restriction != nil {
		switch entityScope.EntityType {
		case queries.EntityTypeResource:
			ids, err := tvm.queries.GetWorkspaceOrganizationIDByResourceID(ctx, entityScope.EntityID)
			if err != nil || ids.EnvironmentID != restriction.EnvironmentID || ids.StackName != restriction.Name {
				return ErrInsufficentPermissions
			}
		case queries.EntityTypeEnvironment:
			if entityScope.EntityID != restriction.EnvironmentID {
				return ErrInsufficentPermissions
			}
		default:
			return ErrInsufficentPermissions
		}
	}

	// hot path: check if token has the entityScope required or has sys:scope
	for _, scope := range givenEntityScopes {
		if scope == entityScope {
			return nil
		}
		// e.g. if operation requires workspace:write and user has sys:write, allow it.
		if scope.EntityType == queries.EntityTypeSystem && scope.Scope == entityScope.Scope {
			return nil
		}
	}

	// not so hot path: check implied scopes via entity hierarchy
	var otherEntityScopes []queries.EntityScope
	switch entityScope.EntityType {
	case queries.EntityTypeOrganization:
		return ErrInsufficentPermissions
	case queries.EntityTypeUser:
		return ErrInsufficentPermissions // nothing higher to check
	case queries.EntityTypeWorkspace:
		orgID, err := tvm.queries.GetOrganizationIDByWorkspaceID(ctx, entityScope.EntityID)
		if err != nil {
			slog.ErrorContext(ctx, err.Error())
			return ErrEntityNotFound
		}
		otherEntityScopes = []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: orgID, Scope: entityScope.Scope},
		}
	case queries.EntityTypeEnvironment:
		ids, err := tvm.queries.GetEnvironmentHierarchy(ctx, entityScope.EntityID)
		if err != nil {
			return ErrEntityNotFound
		}
		otherEntityScopes = []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: ids.OrgID, Scope: entityScope.Scope},
			{EntityType: queries.EntityTypeWorkspace, EntityID: ids.WorkspaceID, Scope: entityScope.Scope},
		}
	case queries.EntityTypeResource:
		ids, err := tvm.queries.GetWorkspaceOrganizationIDByResourceID(ctx, entityScope.EntityID)
		if err != nil {
			slog.ErrorContext(ctx, err.Error())
			return ErrEntityNotFound
		}
		otherEntityScopes = []queries.EntityScope{
			{EntityType: queries.EntityTypeOrganization, EntityID: ids.OrgID, Scope: entityScope.Scope},
			{EntityType: queries.EntityTypeWorkspace, EntityID: ids.WorkspaceID, Scope: entityScope.Scope},
			{EntityType: queries.EntityTypeEnvironment, EntityID: ids.EnvironmentID, Scope: entityScope.Scope},
		}
	default:
		return ErrEntityNotFound
	}

	for _, oes := range otherEntityScopes {
		if slices.Contains(givenEntityScopes, oes) {
			return nil
		}
	}

	return ErrInsufficentPermissions
}

// VerifyWithEntity verifies that the given token has the entityScope required, either explicitly
// or implicitly. Returns the entity associated with the token on success.
func (tvm *VendingMachine) VerifyWithEntity(
	ctx context.Context,
	token string,
	entityScope queries.EntityScope,
) (queries.Entity, error) {
	entity, scopes, err := tvm.GetToken(ctx, token)
	if err != nil {
		return queries.Entity{}, ErrTokenNotFound
	}
	restriction, err := tvm.StackRestriction(ctx, token)
	if err != nil {
		return queries.Entity{}, ErrTokenNotFound
	}
	if restriction != nil {
		ctx = context.WithValue(ctx, contextkeys.StackRestrictionKey, restriction)
	}
	return entity, tvm.VerifyWithGivenEntityScopes(ctx, scopes, entityScope)
}

// Verify verifies that the given token has the entityScope required, either explicitly or implicitly.
func (tvm *VendingMachine) Verify(ctx context.Context, token string, entityScope queries.EntityScope) error {
	_, err := tvm.VerifyWithEntity(ctx, token, entityScope)
	return err
}
