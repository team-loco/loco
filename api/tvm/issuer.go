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

const tokenDisplaySuffixLength = 4

// pgNameEntityUnique is the (name, entity_type, entity_id) unique constraint of api_tokens;
// the token_hash constraint also raises unique violations and must not read as a taken name.
const pgNameEntityUnique = "api_tokens_name_entity_type_entity_id_key"

// Issue stores an API token with its owning entity, scopes and historical issuer.
// The caller must authorize the owner and requested scopes before issuing a token.
func (tvm *VendingMachine) Issue(
	ctx context.Context,
	name string,
	issuer uuid.UUID,
	entity queries.Entity,
	entityScopes []queries.EntityScope,
	duration time.Duration,
) (string, queries.CreateAPITokenRow, error) {
	if duration > tvm.Cfg.MaxAPITokenDuration {
		return "", queries.CreateAPITokenRow{}, ErrDurationExceedsMaxAllowed
	}

	token, hash := generateToken(prefixAPIKey)
	row, err := tvm.queries.CreateAPIToken(ctx, queries.CreateAPITokenParams{
		ID:            uuid.Must(uuid.NewV7()),
		TokenHash:     hash,
		DisplaySuffix: token[len(token)-tokenDisplaySuffixLength:],
		Name:          name,
		EntityType:    entity.Type,
		EntityID:      entity.ID,
		Scopes:        entityScopes,
		CreatedBy:     issuer,
		ExpiresAt:     time.Now().Add(duration),
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
