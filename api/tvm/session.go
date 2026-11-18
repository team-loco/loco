package tvm

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	queries "github.com/team-loco/loco/api/gen/db"
)

func (tvm *VendingMachine) IssueSession(
	ctx context.Context,
	userID uuid.UUID,
	identityID *uuid.UUID,
	ip netip.Addr,
	userAgent string,
) (string, string, error) {
	return tvm.issueSession(ctx, queries.SessionKindCli, userID, identityID, ip, userAgent)
}

func (tvm *VendingMachine) IssueWebSession(
	ctx context.Context,
	userID, identityID uuid.UUID,
	ip netip.Addr,
	userAgent string,
) (string, string, error) {
	return tvm.issueSession(ctx, queries.SessionKindWeb, userID, &identityID, ip, userAgent)
}

func (tvm *VendingMachine) issueSession(
	ctx context.Context,
	kind queries.SessionKind,
	userID uuid.UUID,
	identityID *uuid.UUID,
	ip netip.Addr,
	userAgent string,
) (string, string, error) {
	accessToken, accessHash := generateToken(prefixSession)
	refreshToken, refreshHash := generateToken(prefixRefresh)

	now := time.Now()

	var ipAddr *netip.Addr
	if ip.IsValid() {
		ipAddr = &ip
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
		Kind:             kind,
	}); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrStoreToken, err)
	}
	return accessToken, refreshToken, nil
}
