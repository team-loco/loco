package tvm

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm/providers"
)

// Exchange returns a token for the user with the given email. It is expected that the email has been
// provided by a provider in a trusted manner (e.g., after successful OAuth).
func (tvm *VendingMachine) Exchange(ctx context.Context, email providers.EmailResponse) (queries.User, string, error) {
	address, err := email.Address()
	if err != nil {
		slog.Error(err.Error())
		return queries.User{}, "", ErrExchange
	}

	// get the user and their scopes by their email
	userWithScopes, err := tvm.queries.GetUserWithScopesByEmail(ctx, address)
	if err != nil {
		slog.Error(err.Error())
		return queries.User{}, "", ErrUserNotFound
	}

	// construct user object
	user := queries.User{
		ID:        userWithScopes.ID,
		Email:     userWithScopes.Email,
		Name:      userWithScopes.Name,
		AvatarUrl: userWithScopes.AvatarUrl,
		CreatedAt: userWithScopes.CreatedAt,
		UpdatedAt: userWithScopes.UpdatedAt,
	}

	// issue the token
	token, err := tvm.issueNoCheck(ctx, fmt.Sprintf("login token for user %d created at %s", user.ID, time.Now().Format(time.RFC1123)), queries.Entity{
		Type: queries.EntityTypeUser,
		ID:   user.ID,
	}, userWithScopes.Scopes, tvm.cfg.LoginTokenDuration)
	if err != nil {
		slog.ErrorContext(ctx, err.Error())
		return queries.User{}, "", fmt.Errorf("issue login token: %w", err)
	}

	return user, token, nil
}
