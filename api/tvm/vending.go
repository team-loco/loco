package tvm

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	queries "github.com/team-loco/loco/api/gen/db"
)

// todo config

// Queries is the interface of queries that the Token Vending Machine relies on.
// Usually, *queries.Queries or a fake test database.
type Queries interface {
	GetUserScopes(ctx context.Context, userID int64) ([]queries.UserScope, error)
	StoreToken(ctx context.Context, arg queries.StoreTokenParams) error
	GetToken(ctx context.Context, token string) (queries.GetTokenRow, error)
	GetOrganizationIDByWorkspaceID(ctx context.Context, id int64) (int64, error)
	GetWorkspaceOrganizationIDByAppID(ctx context.Context, id int64) (queries.GetWorkspaceOrganizationIDByAppIDRow, error)
	GetUserScopesByEmail(ctx context.Context, email string) ([]queries.UserScope, error)
}

type VendingMachine struct {
	queries Queries
	cfg     Config
}

type Config struct {
	MaxTokenDuration   time.Duration
	LoginTokenDuration time.Duration
}

// Issue issues a TVM token for the given entity with the given scopes and duration, after verifying
// that the user has sufficient permissions.
func (tvm *VendingMachine) Issue(ctx context.Context, userID int64, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	// gotta make sure the requested duration does not exceed the max allowed duration
	if duration > tvm.cfg.MaxTokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}

	// fetch the scopes associated with the user
	userScopes, err := tvm.queries.GetUserScopes(ctx, userID)
	if err != nil {
		return "", err
	}

	userEntityScopes := queries.EntityScopesFromUserScopes(userScopes)

	// verify that the user has sufficient permissions to issue a token with the requested scopes
	for _, entityScope := range entityScopes {
		if !slices.Contains(userEntityScopes, entityScope) {
			return "", ErrInsufficentPermissions
		}
	}

	return tvm.issueNoCheck(ctx, entity, entityScopes, duration)
}

// issueNoCheck issues a token without checking permissions.
func (tvm *VendingMachine) issueNoCheck(ctx context.Context, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	tk := uuid.Must(uuid.NewV7())
	tks := tk.String()

	// issue the token
	err := tvm.queries.StoreToken(ctx, queries.StoreTokenParams{
		Token:      tks,
		EntityType: queries.EntityType(entity.Type),
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		ExpiresAt:  time.Now().Add(duration),
	})
	if err != nil {
		return "", ErrStoreToken
	}

	return tks, nil
}

func (tvm *VendingMachine) Verify(ctx context.Context, token string, entityScope queries.EntityScope) error {
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		return ErrTokenNotFound
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return ErrTokenExpired
	}

	// hot path: check if token has the entityScope required or has sys:scope
	for _, scope := range tokenData.Scopes {
		if scope == entityScope { // the token directly has the scope needed
			return nil
		}
		// for example: if operation requires workspace:write and user has sys:write
		// it should allow the operation. this function still does not allow access
		// for someone with something like sys:read to workspace:write.
		if scope.Entity.Type == queries.EntityTypeSystem && scope.Scope == entityScope.Scope {
			return nil
		}
	}
	// not so hot path: if the token has an entityScope that is *implied*
	var otherEntityScopes []queries.EntityScope
	switch entityScope.Entity.Type {
	case queries.EntityTypeOrganization, queries.EntityTypeUser:
		return ErrInsufficentPermissions // there is nothing higher to check. if doesn't have org or sys permissions for scope on an org, you don't have enough perms.
	case queries.EntityTypeWorkspace:
		// lookup the org id for the workspace
		org_id, err := tvm.queries.GetOrganizationIDByWorkspaceID(ctx, entityScope.Entity.ID)
		if err != nil {
			// note: this could be another error
			return ErrEntityNotFound
		}

		// check for org:scope
		otherEntityScopes = []queries.EntityScope{
			{
				Entity: queries.Entity{
					Type: queries.EntityTypeOrganization,
					ID:   org_id,
				},
				Scope: entityScope.Scope,
			},
		}
	case queries.EntityTypeApp:
		// lookup the workspace and org id for the app
		ids, err := tvm.queries.GetWorkspaceOrganizationIDByAppID(ctx, entityScope.Entity.ID)
		if err != nil {
			// note: again this could be another eror
			return ErrEntityNotFound
		}
		wks_id := ids.WorkspaceID
		org_id := ids.OrgID

		// check for org:scope and workspace:scope
		otherEntityScopes = []queries.EntityScope{
			{
				Entity: queries.Entity{
					Type: queries.EntityTypeOrganization,
					ID:   org_id,
				},
				Scope: entityScope.Scope,
			},
			{
				Entity: queries.Entity{
					Type: queries.EntityTypeWorkspace,
					ID:   wks_id,
				},
				Scope: entityScope.Scope,
			},
		}
	default:
		return ErrEntityNotFound // unknown entity type
	}

	// check otherentityscopes. note: someone see if this can be optimized
	for _, oes := range otherEntityScopes {
		// if token has any of the implied scopes, allow
		if slices.Contains(tokenData.Scopes, oes) {
			return nil
		}
	}

	return ErrInsufficentPermissions
}

func NewVendingMachine(queries Queries, cfg Config) *VendingMachine {
	return &VendingMachine{
		queries: queries,
		cfg:     cfg,
	}
}

// verifywithidentity returns identity and error
// revoketoken revokes a token
// updaterole
