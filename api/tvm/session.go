package tvm

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
	queries "github.com/team-loco/loco/api/gen/db"
)

func (tvm *VendingMachine) IssueSession(
	ctx context.Context,
	userID uuid.UUID,
	identityID *uuid.UUID,
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
	}); err != nil {
		slog.ErrorContext(ctx, "failed to create session token", "error", err)
		return "", "", ErrStoreToken
	}
	return accessToken, refreshToken, nil
}
