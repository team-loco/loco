package tvm

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/google/uuid"
	queries "github.com/loco-team/loco/api/gen/db"
)

// todo config
type VendingMachine struct {
	queries *queries.Queries
	secret  []byte
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

func NewVendingMachine(queries *queries.Queries, cfg Config) *VendingMachine {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) == 0 {
		panic("JWT_SECRET not set")
	}
	return &VendingMachine{
		queries: queries,
		secret:  []byte(secret),
		cfg:     cfg,
	}
}
