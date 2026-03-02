package tvm

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm/providers"
)

// Exchange authenticates a user via their OAuth-provided email and issues a new
// session token pair (access + refresh). ip and userAgent are stored for session
// display; either may be empty.
func (tvm *VendingMachine) Exchange(ctx context.Context, email providers.EmailResponse, ip string, userAgent string) (queries.User, string, string, error) {
	address, err := email.Address()
	if err != nil {
		slog.Error(err.Error())
		return queries.User{}, "", "", ErrExchange
	}

	userWithScopes, err := tvm.queries.GetUserWithScopesByEmail(ctx, address)
	if err != nil {
		slog.Error(err.Error())
		return queries.User{}, "", "", ErrUserNotFound
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

	var ua pgtype.Text
	if userAgent != "" {
		ua = pgtype.Text{String: userAgent, Valid: true}
	}

	if err := tvm.queries.CreateSessionToken(ctx, queries.CreateSessionTokenParams{
		ID:               uuid.Must(uuid.NewV7()),
		AccessTokenHash:  accessHash,
		RefreshTokenHash: refreshHash,
		UserID:           user.ID,
		AccessExpiresAt:  pgtype.Timestamptz{Time: now.Add(tvm.Cfg.SessionAccessTokenDuration), Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: now.Add(tvm.Cfg.SessionRefreshTokenDuration), Valid: true},
		IpAddress:        ipAddr,
		UserAgent:        ua,
	}); err != nil {
		slog.ErrorContext(ctx, fmt.Sprintf("failed to create session token: %s", err.Error()))
		return queries.User{}, "", "", ErrStoreToken
	}

	return user, accessToken, refreshToken, nil
}
