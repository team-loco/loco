package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// ErrAccountUnavailable must not be treated as a missing sign-in identity that
// permits provisioning a replacement account during an existing-account mutation.
var ErrAccountUnavailable = errors.New("the signed-in account is no longer available")

func lockActiveUser(ctx context.Context, qtx *genDb.Queries, userID uuid.UUID) (genDb.User, error) {
	if err := authz.LockUsers(ctx, qtx, userID); errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, ErrAccountUnavailable
	} else if err != nil {
		return genDb.User{}, fmt.Errorf("lock signed-in user: %w", err)
	}
	user, err := qtx.GetUserByID(ctx, userID)
	if err != nil {
		return genDb.User{}, fmt.Errorf("read locked user: %w", err)
	}
	if user.Status != genDb.UserStatusActive {
		return genDb.User{}, ErrAccountUnavailable
	}
	return user, nil
}
