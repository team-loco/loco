package tvm

import (
	"context"
	"os"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/loco-team/loco/api/db"
	queries "github.com/loco-team/loco/api/gen/db"
)

type Claims struct {
	EntityScopes []EntityScope `json:"scopes"` // entity scopes associated with the token
	jwt.RegisteredClaims
}

type VendingMachine struct {
	db               *db.DB
	queries          *queries.Queries
	secret           []byte
	maxTokenDuration time.Duration
}

func (tvm *VendingMachine) Issue(ctx context.Context, userID int64, entity Entity, entityScopes []EntityScope, duration time.Duration) (string, error) {
	// gotta make sure the requested duration does not exceed the max allowed duration
	if duration > tvm.maxTokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}

	// fetch the scopes associated with the user
	userScopes, err := tvm.queries.GetUserScopes(ctx, userID)
	if err != nil {
		return "", err
	}

	// verify that the user has sufficient permissions to issue a token with the requested scopes
	for _, entityScope := range entityScopes {
		if entityScope.Entity.Type == EntityTypeUser {
			if entityScope.Entity.ID == userID {
				continue
			}
			return "", ErrInsufficentPermissions
		}
		if !slices.Contains(userScopes, queries.GetUserScopesRow{
			Scope:      entityScope.Scope,
			EntityType: queries.EntityType(entityScope.Entity.Type),
			EntityID:   entityScope.Entity.ID,
		}) {
			return "", ErrInsufficentPermissions
		}
	}

	// issue the token
	tvm.queries.StoreToken(ctx, queries.StoreTokenParams{
		EntityType: queries.EntityType(entity.Type),
		EntityID:   entity.ID,
		Scopes: entityScopes
	})
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
