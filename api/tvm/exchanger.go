package tvm

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm/providers"
)

// Exchange authenticates a user via their OAuth-provided email and issues a new
// session token pair (access + refresh). ip and userAgent are stored for session
// display; either may be empty.
func (tvm *VendingMachine) Exchange(
	ctx context.Context,
	email providers.EmailResponse,
	ip string,
	userAgent string,
) (queries.User, string, string, error) {
	address, err := email.Address()
	if err != nil {
		slog.ErrorContext(ctx, "failed to read email from external provider", "error", err)
		return queries.User{}, "", "", ErrExchange
	}

	userWithScopes, err := tvm.queries.GetUserWithScopesByEmail(ctx, address)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.DebugContext(ctx, "no user for email", "email", address)
		return queries.User{}, "", "", ErrUserNotFound
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up user by email", "error", err)
		return queries.User{}, "", "", ErrUserLookup
	}

	user := queries.User{
		ID:        userWithScopes.ID,
		Email:     userWithScopes.Email,
		Name:      userWithScopes.Name,
		AvatarUrl: userWithScopes.AvatarUrl,
		CreatedAt: userWithScopes.CreatedAt,
		UpdatedAt: userWithScopes.UpdatedAt,
	}

	accessToken, accessHash := generateToken(prefixSession)
	refreshToken, refreshHash := generateToken(prefixRefresh)

	now := time.Now()

	var ipAddr *netip.Addr
	if ip != "" {
		parsed, parseErr := netip.ParseAddr(ip)
		if parseErr == nil {
			ipAddr = &parsed
		}
	}

	if err := tvm.queries.CreateSessionToken(ctx, queries.CreateSessionTokenParams{
		ID:               uuid.Must(uuid.NewV7()),
		AccessTokenHash:  accessHash,
		RefreshTokenHash: refreshHash,
		UserID:           user.ID,
		AccessExpiresAt:  now.Add(tvm.Cfg.SessionAccessTokenDuration),
		RefreshExpiresAt: now.Add(tvm.Cfg.SessionRefreshTokenDuration),
		IpAddress:        ipAddr,
		UserAgent:        &userAgent,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to create session token", "error", err)
		return queries.User{}, "", "", ErrStoreToken
	}

	return user, accessToken, refreshToken, nil
}
