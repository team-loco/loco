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
	subject, err := identity.Subject()
	if err != nil || subject == "" {
		slog.ErrorContext(ctx, "failed to read account id from external provider", "error", err)
		return queries.User{}, "", "", ErrExchange
	}
	address, err := identity.Address()
	if err != nil || address == "" {
		slog.ErrorContext(ctx, "failed to read email from external provider", "error", err)
		return queries.User{}, "", "", ErrExchange
	}

	user, err := tvm.queries.GetUserByIdentity(ctx, queries.GetUserByIdentityParams{
		Issuer:  identity.Issuer(),
		Subject: subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		slog.DebugContext(ctx, "no user for identity", "issuer", identity.Issuer(), "subject", subject)
		return queries.User{}, "", "", ErrUserNotFound
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up user by identity", "error", err)
		return queries.User{}, "", "", ErrUserLookup
	}

	if touchErr := tvm.queries.TouchIdentity(ctx, queries.TouchIdentityParams{
		Issuer:        identity.Issuer(),
		Subject:       subject,
		Email:         &address,
		EmailVerified: identity.EmailVerified(),
	}); touchErr != nil {
		slog.WarnContext(ctx, "failed to record identity login", "userId", user.ID, "error", touchErr)
	}

	if user.Email != address {
		user = tvm.syncUserEmail(ctx, user, address)
	}

	accessToken, refreshToken, err := tvm.IssueSession(ctx, user.ID, nil, nil, ip, userAgent)
	if err != nil {
		return queries.User{}, "", "", err
	}

	return user, accessToken, refreshToken, nil
}

func (tvm *VendingMachine) IssueSession(
	ctx context.Context,
	userID uuid.UUID,
	identityID *uuid.UUID,
	ssoConnection *string,
	ip string,
	userAgent string,
) (string, string, error) {
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
		UserID:           userID,
		AccessExpiresAt:  now.Add(tvm.Cfg.SessionAccessTokenDuration),
		RefreshExpiresAt: now.Add(tvm.Cfg.SessionRefreshTokenDuration),
		IpAddress:        ipAddr,
		UserAgent:        &userAgent,
		IdentityID:       identityID,
		SsoConnectionID:  ssoConnection,
	}); err != nil {
		slog.ErrorContext(ctx, "failed to create session token", "error", err)
		return "", "", ErrStoreToken
	}
	return accessToken, refreshToken, nil
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
