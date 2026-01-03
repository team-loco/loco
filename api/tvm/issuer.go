package tvm

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	queries "github.com/team-loco/loco/api/gen/db"
)

// Issue issues a token associated with the given entity and scopes for the given duration. The userID is the ID of the user requesting the token.
// The user must have sufficient permissions to issue a token with the requested scopes, or an error [ErrInsufficentPermissions] is returned.
// It is important to note that this function does NOT verify that whatever is requesting the token is the user with the given userID. It is expected that the caller
// has already verified this.
func (tvm *VendingMachine) Issue(ctx context.Context, name string, userID int64, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	// gotta make sure the requested duration does not exceed the max allowed duration
	if duration > tvm.cfg.MaxTokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}

	// fetch the scopes associated with the user
	userScopes, err := tvm.queries.GetUserScopes(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", err
	}

	// verify that the user has all the requested scopes, either explicitly or implicitly
	for _, entityScope := range entityScopes {
		if err := tvm.VerifyWithGivenEntityScopes(ctx, userScopes, entityScope); err != nil {
			slog.ErrorContext(ctx, err.Error())
			return "", err
		}
	}

	return tvm.issueNoCheck(ctx, name, entity, entityScopes, duration)
}

// IssueWithLoginToken issues a token associated with the given entity and scopes for the given duration, using a login token for authentication. The login token must be
// associated with a user, and that user must have sufficient permissions to issue a token with the requested scopes, or an error [ErrInsufficentPermissions] is returned.
// Unlike [Issue], this function uses a token to authenticate the user, rather than taking a userID directly.
func (tvm *VendingMachine) IssueWithLoginToken(ctx context.Context, name string, token string, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	// this is meant to issue a token from a user's login token, although a user token could also be used
	tokenData, err := tvm.queries.GetToken(ctx, token)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", ErrTokenNotFound
	}
	if time.Now().After(tokenData.ExpiresAt) {
		return "", ErrTokenExpired
	}
	if tokenData.EntityType != queries.EntityTypeUser {
		return "", ErrImproperUsage
	}
	userID := tokenData.EntityID

	return tvm.Issue(ctx, name, userID, entity, entityScopes, duration)
}

// issueNoCheck issues a token without checking permissions.
func (tvm *VendingMachine) issueNoCheck(ctx context.Context, name string, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	tk := uuid.Must(uuid.NewV7())
	tks := tk.String()

	// issue the token
	err := tvm.queries.StoreToken(ctx, queries.StoreTokenParams{
		Name:       name,
		Token:      tks,
		EntityType: queries.EntityType(entity.Type),
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		ExpiresAt:  time.Now().Add(duration),
	})
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", ErrStoreToken
	}

	return tks, nil
}
