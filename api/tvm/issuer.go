package tvm

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	queries "github.com/team-loco/loco/api/gen/db"
)

// Issue issues an API token for the given entity and scopes. The userID is the ID of the
// user requesting the token; that user must already hold all the requested scopes (explicitly
// or implicitly), otherwise ErrInsufficentPermissions is returned.
// It is the caller's responsibility to verify that the request is coming from the user with
// this userID before calling Issue.
func (tvm *VendingMachine) Issue(
	ctx context.Context,
	name string,
	userID string,
	entity queries.Entity,
	entityScopes []queries.EntityScope,
	duration time.Duration,
	stack ...string,
) (string, error) {
	if len(stack) > 1 {
		return "", ErrImproperUsage
	}
	stackName := ""
	if len(stack) == 1 {
		stackName = stack[0]
	}
	if stackName != "" {
		if entity.Type != queries.EntityTypeEnvironment {
			return "", ErrImproperUsage
		}
		for _, scope := range entityScopes {
			if scope.EntityType != queries.EntityTypeEnvironment || scope.EntityID != entity.ID {
				return "", ErrInsufficentPermissions
			}
		}
	}
	if duration > tvm.Cfg.MaxAPITokenDuration {
		return "", ErrDurationExceedsMaxAllowed
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return "", err
	}

	userScopes, err := tvm.userScopes(ctx, userUUID)
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

	return tvm.issueAPITokenNoCheck(ctx, name, userUUID, entity, entityScopes, duration, stackName)
}

// IssueWithSessionToken issues an API token using a session token for authentication.
// The session token must belong to a user, and that user must hold all requested scopes.
func (tvm *VendingMachine) IssueWithSessionToken(
	ctx context.Context,
	name string,
	sessionToken string,
	entity queries.Entity,
	entityScopes []queries.EntityScope,
	duration time.Duration,
	stack ...string,
) (string, error) {
	entity2, _, err := tvm.GetToken(ctx, sessionToken)
	if err != nil {
		return "", ErrInvalidExpiredToken
	}
	if entity2.Type != queries.EntityTypeUser {
		return "", ErrImproperUsage
	}
	return tvm.Issue(ctx, name, entity2.ID.String(), entity, entityScopes, duration, stack...)
}

// issueAPITokenNoCheck issues an API token without checking permissions.
func (tvm *VendingMachine) issueAPITokenNoCheck(
	ctx context.Context,
	name string,
	createdBy uuid.UUID,
	entity queries.Entity,
	entityScopes []queries.EntityScope,
	duration time.Duration,
	stackName string,
) (string, error) {
	token, hash := generateToken(prefixAPIKey)

	if err := tvm.queries.CreateAPIToken(ctx, queries.CreateAPITokenParams{
		ID:         uuid.Must(uuid.NewV7()),
		TokenHash:  hash,
		Name:       name,
		StackName:  stackName,
		EntityType: entity.Type,
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		CreatedBy:  createdBy,
		ExpiresAt:  time.Now().Add(duration),
	}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return "", ErrTokenNameTaken
		}
		slog.ErrorContext(ctx, err.Error())
		return "", ErrStoreToken
	}

	return token, nil
}
