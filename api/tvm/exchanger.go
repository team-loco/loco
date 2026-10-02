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

// Exchange authenticates a user via their OAuth provider identity and issues a new
// session token pair (access + refresh). ip and userAgent are stored for session
// display; either may be empty.
func (tvm *VendingMachine) Exchange(
	ctx context.Context,
	identity providers.EmailResponse,
	ip string,
	userAgent string,
) (queries.User, string, string, error) {
	externalID, err := identity.ExternalID()
	if err != nil || externalID == "" {
		slog.ErrorContext(ctx, "failed to read account id from external provider", "error", err)
		return queries.User{}, "", "", ErrExchange
	}
	address, err := identity.Address()
	if err != nil || address == "" {
		slog.ErrorContext(ctx, "failed to read email from external provider", "error", err)
		return queries.User{}, "", "", ErrExchange
	}

	user, err := tvm.queries.GetUserByExternalID(ctx, externalID)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.DebugContext(ctx, "no user for external id", "externalId", externalID)
		return queries.User{}, "", "", ErrUserNotFound
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up user by external id", "error", err)
		return queries.User{}, "", "", ErrUserLookup
	}

	if user.Email != address {
		user = tvm.syncUserEmail(ctx, user, address)
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

func (tvm *VendingMachine) syncUserEmail(ctx context.Context, user queries.User, address string) queries.User {
	updated, err := tvm.queries.UpdateUserEmail(ctx, queries.UpdateUserEmailParams{
		ID:    user.ID,
		Email: address,
	})
	if err != nil {
		slog.WarnContext(ctx, "failed to update user email from provider", "userId", user.ID, "error", err)
		return user
	}
	return updated
}
