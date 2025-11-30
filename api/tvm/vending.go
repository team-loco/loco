package tvm

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/loco-team/loco/api/db"
	queries "github.com/loco-team/loco/api/gen/db"
)

// todo config
type VendingMachine struct {
	db               *db.DB
	queries          *queries.Queries
	secret           []byte
	maxTokenDuration time.Duration
}

func (tvm *VendingMachine) Issue(ctx context.Context, userID int64, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	// gotta make sure the requested duration does not exceed the max allowed duration
	if duration > tvm.maxTokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}
	expiresAt := time.Now().Add(duration)

	// fetch the scopes associated with the user
	userScopes, err := tvm.queries.GetUserScopes(ctx, userID)
	if err != nil {
		return "", err
	}

	// verify that the user has sufficient permissions to issue a token with the requested scopes
	for _, entityScope := range entityScopes {
		if entityScope.Entity.Type == queries.EntityTypeUser {
			if entityScope.Entity.ID == userID { // users can issue tokens on behalf of themselves
				continue
			}
			return "", ErrInsufficentPermissions // users cannot have scopes on other users
		}
		if !slices.Contains(userScopes, queries.GetUserScopesRow{
			Scope:      string(entityScope.Scope),
			EntityType: entityScope.Entity.Type,
			EntityID:   entityScope.Entity.ID,
		}) {
			return "", ErrInsufficentPermissions
		}
	}

	tk := uuid.Must(uuid.NewV7())
	tks := tk.String()

	// issue the token
	err = tvm.queries.StoreToken(ctx, queries.StoreTokenParams{
		Token:      tks,
		EntityType: queries.EntityType(entity.Type),
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		return "", ErrStoreToken
	}

	return tks, nil
}

func (tvm *VendingMachine) VerifyAccess(ctx context.Context, token string, scopesForAction []queries.EntityScope) error {
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		return ErrTokenNotFound
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return ErrTokenExpired
	}

	// scopesForAction is an OR list - any one of the scopes is sufficient to perform the action
	for _, scopeForAction := range scopesForAction {
		if slices.Contains(tokenData.Scopes, scopeForAction) {
			// the token has at least one of the entity scopes, therefore the token
			// is sufficient to perform the action
			return nil
		}
	}

	return ErrInsufficentPermissions
}

// validate
// revoke

func NewVendingMachine(db *db.DB) *VendingMachine {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) == 0 {
		panic("JWT_SECRET not set")
	}
	return &VendingMachine{
		db:     db,
		secret: []byte(secret),
	}
}
