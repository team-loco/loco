package tvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	queries "github.com/team-loco/loco/api/gen/db"
)

// pgNameEntityUnique is the (name, entity_type, entity_id) unique constraint of api_tokens;
// the token_hash constraint also raises unique violations and must not read as a taken name.
const pgNameEntityUnique = "api_tokens_name_entity_type_entity_id_key"

// Issue issues an API token for the given entity and scopes and returns it with the stored
// token metadata. The user is the one requesting the token; that user must already hold all
// the requested scopes (explicitly or implicitly), otherwise authz.ErrInsufficientPermissions
// is returned. It is the caller's responsibility to verify that the request is coming from
// this user before calling Issue.
func (tvm *VendingMachine) Issue(
	ctx context.Context,
	name string,
	user uuid.UUID,
	entity queries.Entity,
	entityScopes []queries.EntityScope,
	duration time.Duration,
) (string, queries.CreateAPITokenRow, error) {
	if duration > tvm.Cfg.MaxAPITokenDuration {
		return "", queries.CreateAPITokenRow{}, ErrDurationExceedsMaxAllowed
	}

	userScopes, err := tvm.authz.UserScopes(ctx, user)
	if err != nil {
		return "", queries.CreateAPITokenRow{}, fmt.Errorf("load scopes of the issuing user: %w", err)
	}

	for _, es := range entityScopes {
		if err = tvm.authz.Check(ctx, userScopes, es); err != nil {
			return "", queries.CreateAPITokenRow{}, err
		}
	}

	token, hash := generateToken(prefixAPIKey)
	row, err := tvm.queries.CreateAPIToken(ctx, queries.CreateAPITokenParams{
		ID:         uuid.Must(uuid.NewV7()),
		TokenHash:  hash,
		Name:       name,
		EntityType: entity.Type,
		EntityID:   entity.ID,
		Scopes:     entityScopes,
		CreatedBy:  user,
		ExpiresAt:  time.Now().Add(duration),
	})
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.Code == "23505" && pgErr.ConstraintName == pgNameEntityUnique {
			return "", queries.CreateAPITokenRow{}, ErrTokenNameTaken
		}
		return "", queries.CreateAPITokenRow{}, fmt.Errorf("%w: %w", ErrStoreToken, err)
	}

	return token, row, nil
}
