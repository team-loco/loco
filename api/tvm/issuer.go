package tvm

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	queries "github.com/team-loco/loco/api/gen/db"
)

// Issue issues an API token for the given entity and scopes. The userID is the ID of the
// user requesting the token; that user must already hold all the requested scopes (explicitly
// or implicitly), otherwise ErrInsufficentPermissions is returned.
// It is the caller's responsibility to verify that the request is coming from the user with
// this userID before calling Issue.
func (tvm *VendingMachine) Issue(ctx context.Context, name string, userID string, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	if duration > tvm.Cfg.MaxAPITokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", err
	}

	userScopes, err := tvm.queries.GetUserScopes(ctx, userUUID)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", err
	}

	for _, es := range entityScopes {
		if err := tvm.VerifyWithGivenEntityScopes(ctx, userScopes, es); err != nil {
			slog.ErrorContext(ctx, err.Error())
			return "", err
		}
	}

	return tvm.issueAPITokenNoCheck(ctx, name, userUUID, entity, entityScopes, duration)
}

// IssueWithSessionToken issues an API token using a session token for authentication.
// The session token must belong to a user, and that user must hold all requested scopes.
func (tvm *VendingMachine) IssueWithSessionToken(ctx context.Context, name string, sessionToken string, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	entity2, _, err := tvm.GetToken(ctx, sessionToken)
	if err != nil {
		return "", ErrInvalidExpiredToken
	}
	if entity2.Type != queries.EntityTypeUser {
		return "", ErrImproperUsage
	}
	return tvm.Issue(ctx, name, entity2.ID.String(), entity, entityScopes, duration)
}

// issueAPITokenNoCheck issues an API token without checking permissions.
func (tvm *VendingMachine) issueAPITokenNoCheck(ctx context.Context, name string, createdBy uuid.UUID, entity queries.Entity, entityScopes []queries.EntityScope, duration time.Duration) (string, error) {
	token, hash := generateToken(prefixAPIKey)

	if err := tvm.queries.CreateAPIToken(ctx, queries.CreateAPITokenParams{
		ID:         uuid.Must(uuid.NewV7()),
		TokenHash:  hash,
		Name:       name,
		EntityType: entity.Type,
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		CreatedBy:  createdBy,
		ExpiresAt:  time.Now().Add(duration),
	}); err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", ErrStoreToken
	}

	return token, nil
}
