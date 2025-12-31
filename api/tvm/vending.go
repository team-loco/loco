package tvm

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	DeleteToken(ctx context.Context, token string) error
	GetUserScopesOnEntity(ctx context.Context, arg queries.GetUserScopesOnEntityParams) ([]string, error)
	GetUserScopesOnOrganization(ctx context.Context, arg queries.GetUserScopesOnOrganizationParams) ([]queries.GetUserScopesOnOrganizationRow, error)
	GetUserScopesOnWorkspace(ctx context.Context, arg queries.GetUserScopesOnWorkspaceParams) ([]queries.GetUserScopesOnWorkspaceRow, error)
	AddUserScope(ctx context.Context, arg queries.AddUserScopeParams) error
	RemoveUserScope(ctx context.Context, arg queries.RemoveUserScopeParams) error
}

type VendingMachine struct {
	pool    *pgxpool.Pool
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

// IssueWithLoginToken issues a TVM token. For more, see [Issue]. The provided token must be a token issued for a user (i.e. a login token).
func (tvm *VendingMachine) IssueWithLoginToken(ctx context.Context, token string, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	// this is meant to issue a token from a user's login token, although an user token could also be used
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		return "", ErrTokenNotFound
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return "", ErrTokenExpired
	}
	if tokenData.EntityType != queries.EntityTypeUser {
		return "", ErrImproperUsage
	}
	userID := tokenData.EntityID

	return tvm.Issue(ctx, userID, entity, entityScopes, duration)
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

// Verify verifies that the given token has the entityScope required, either explicitly or implicitly. It returns an error if an error
// occurs, if the entity does not exist [ErrEntityNotFound], or if the token does not have sufficient permissions [ErrInsufficentPermissions].
// It also returns the entity associated with the token, for example, log in tokens will return the user entity, and tokens issues for an
// application will return the application entity.
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

	// hot path: check if token has the entityScope required or has sys:scope
	for _, scope := range tokenData.Scopes {
		if scope == entityScope { // the token directly has the scope needed
			return tokenEntity, nil
		}
		// for example: if operation requires workspace:write and user has sys:write
		// it should allow the operation. this function still does not allow access
		// for someone with something like sys:read to workspace:write.
		if scope.Entity.Type == queries.EntityTypeSystem && scope.Scope == entityScope.Scope {
			return tokenEntity, nil
		}
	}
	// not so hot path: if the token has an entityScope that is *implied*
	var otherEntityScopes []queries.EntityScope
	switch entityScope.Entity.Type {
	case queries.EntityTypeOrganization, queries.EntityTypeUser:
		return tokenEntity, ErrInsufficentPermissions // there is nothing higher to check. if doesn't have org or sys permissions for scope on an org, you don't have enough perms.
	case queries.EntityTypeWorkspace:
		// lookup the org id for the workspace
		org_id, err := tvm.queries.GetOrganizationIDByWorkspaceID(ctx, entityScope.Entity.ID)
		if err != nil {
			// note: this could be another error
			return tokenEntity, ErrEntityNotFound
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
			return tokenEntity, ErrEntityNotFound
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
		return tokenEntity, ErrEntityNotFound // unknown entity type
	}

	// check otherentityscopes. note: someone see if this can be optimized
	for _, oes := range otherEntityScopes {
		// if token has any of the implied scopes, allow
		if slices.Contains(tokenData.Scopes, oes) {
			return tokenEntity, nil
		}
	}

	return tokenEntity, ErrInsufficentPermissions
}

// Verify verifies that the given token has the entityScope required, either explicitly or implicitly. It returns an error if an error
// occurs, if the entity does not exist [ErrEntityNotFound], or if the token does not have sufficient permissions [ErrInsufficentPermissions].
func (tvm *VendingMachine) Verify(ctx context.Context, token string, entityScope queries.EntityScope) error {
	_, err := tvm.VerifyWithEntity(ctx, token, entityScope)
	return err
}

// Revoke deletes the given token, effectively immediately revoking it.
func (tvm *VendingMachine) Revoke(ctx context.Context, token string) error {
	return tvm.queries.DeleteToken(ctx, token)
}

// GetRoles returns all roles for the given user. The token must have user:read.
func (tvm *VendingMachine) GetRoles(ctx context.Context, token string, userID int64) ([]queries.EntityScope, error) {
	if err := tvm.Verify(ctx, token, queries.EntityScope{
		Entity: queries.Entity{
			Type: queries.EntityTypeUser,
			ID:   userID,
		},
		Scope: queries.ScopeRead,
	}); err != nil {
		return nil, err
	}

	userScopes, err := tvm.queries.GetUserScopes(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user scopes: %w", err)
	}

	// convert userScopes to entityScopes (they're the same thing really)
	entityScopes := []queries.EntityScope{}
	for _, userScope := range userScopes {
		entityScopes = append(entityScopes, queries.EntityScope{
			Entity: queries.Entity{
				Type: userScope.EntityType,
				ID:   userScope.EntityID,
			},
			Scope: userScope.Scope,
		})
	}

	return entityScopes, nil
}

// GetRolesByEntity returns all roles for the given entity and below for the given user. The token must have read on the given entity.
func (tvm *VendingMachine) GetRolesByEntity(ctx context.Context, token string, userID int64, entity queries.Entity) ([]queries.EntityScope, error) {
	// returns all roles for the given entity and below (so if entity is org, returns org, workspace, app roles that are explicitly listed)

	// must have read on the entity
	if err := tvm.Verify(ctx, token, queries.EntityScope{
		Entity: entity,
		Scope:  queries.ScopeRead,
	}); err != nil {
		return nil, err
	}

	switch entity.Type {
	case queries.EntityTypeOrganization:
		// organization: get all org, workspace, app roles
		rows, err := tvm.queries.GetUserScopesOnOrganization(ctx, queries.GetUserScopesOnOrganizationParams{
			UserID: userID,
			ID:     entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on organization: %w", err)
		}
		entityScopes := []queries.EntityScope{}
		for _, row := range rows {
			entityScopes = append(entityScopes, queries.EntityScope{
				Entity: queries.Entity{
					Type: row.EntityType,
					ID:   row.EntityID,
				},
				Scope: row.Scope,
			})
		}
		return entityScopes, nil
	case queries.EntityTypeWorkspace:
		// workspace: get all workspace, app roles
		rows, err := tvm.queries.GetUserScopesOnWorkspace(ctx, queries.GetUserScopesOnWorkspaceParams{
			UserID: userID,
			ID:     entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on workspace: %w", err)
		}
		entityScopes := []queries.EntityScope{}
		for _, row := range rows {
			entityScopes = append(entityScopes, queries.EntityScope{
				Entity: queries.Entity{
					Type: row.EntityType,
					ID:   row.EntityID,
				},
				Scope: row.Scope,
			})
		}
		return entityScopes, nil
	case queries.EntityTypeApp:
		// app: just get app roles
		fallthrough
	default:
		userScopes, err := tvm.queries.GetUserScopesOnEntity(ctx, queries.GetUserScopesOnEntityParams{
			UserID:     userID,
			EntityType: entity.Type,
			EntityID:   entity.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("get user scopes on entity: %w", err)
		}
		entityScopes := []queries.EntityScope{}
		for _, scope := range userScopes {
			entityScopes = append(entityScopes, queries.EntityScope{
				Entity: entity,
				Scope:  scope,
			})
		}
		return entityScopes, nil
	}
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
		if !slices.Contains(entities, es.Entity) {
			entities = append(entities, es.Entity)
		}
	}
	// verify that the token has admin on all of these entities
	for _, entity := range entities {
		err := tvm.Verify(ctx, token, queries.EntityScope{
			Entity: entity,
			Scope:  queries.ScopeAdmin,
		})
		if err != nil { // insufficient permissions or other error, bye bye no update 4 u
			return err
		}
	}
	// token has admin on all entities, proceed with update

	tx, err := tvm.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, es := range addScopes {
		if err := tvm.queries.AddUserScope(ctx, queries.AddUserScopeParams{
			UserID:     userID,
			EntityType: es.Entity.Type,
			EntityID:   es.Entity.ID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("add user scope: %w", err)
		}
	}
	for _, es := range removeScopes {
		if err := tvm.queries.RemoveUserScope(ctx, queries.RemoveUserScopeParams{
			UserID:     userID,
			EntityType: es.Entity.Type,
			EntityID:   es.Entity.ID,
			Scope:      es.Scope,
		}); err != nil {
			return fmt.Errorf("remove user scope: %w", err)
		}
	}
	err = tx.Commit(ctx)
	if err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func NewVendingMachine(pool *pgxpool.Pool, queries Queries, cfg Config) *VendingMachine {
	return &VendingMachine{
		pool:    pool,
		queries: queries,
		cfg:     cfg,
	}
}

// verifywithidentity returns identity and error
// revoketoken revokes a token
// updaterole
