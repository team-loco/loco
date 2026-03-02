package tvm

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	queries "github.com/team-loco/loco/api/gen/db"
)

// GetToken validates a token (session or API) and returns the entity it represents
// along with its effective scopes.
//
// Session tokens (loco_s_...): scopes are always fetched live from user_scopes so
// permission changes take effect immediately.
//
// API tokens (loco_k_...): scopes are the baked-in set from creation and never change.
//
// Refresh tokens (loco_r_...) are rejected — they are not valid access credentials.
func (tvm *VendingMachine) GetToken(ctx context.Context, token string) (queries.Entity, []queries.EntityScope, error) {
	hash := hashToken(token)

	switch tokenPrefix(token) {
	case prefixSession:
		session, err := tvm.queries.GetSessionByAccessToken(ctx, hash)
		if err != nil {
			return queries.Entity{}, nil, ErrInvalidExpiredToken
		}

		entity := queries.Entity{
			Type: queries.EntityTypeUser,
			ID:   session.UserID,
		}

		liveScopes, err := tvm.queries.GetUserScopes(ctx, session.UserID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to fetch live scopes for session token", "err", err)
			return queries.Entity{}, nil, err
		}

		tvm.touchSessionLastUsed(ctx, session.ID, session.LastUsedAt)

		return entity, liveScopes, nil

	case prefixAPIKey:
		tok, err := tvm.queries.GetAPIToken(ctx, hash)
		if err != nil {
			return queries.Entity{}, nil, ErrInvalidExpiredToken
		}

		entity := queries.Entity{
			Type: tok.EntityType,
			ID:   tok.EntityID,
		}

		tvm.touchAPITokenLastUsed(ctx, tok.ID, tok.LastUsedAt)

		return entity, tok.Scopes, nil

	default:
		return queries.Entity{}, nil, ErrInvalidExpiredToken
	}
}

// Revoke immediately invalidates the given token.
func (tvm *VendingMachine) Revoke(ctx context.Context, token string) error {
	hash := hashToken(token)
	switch tokenPrefix(token) {
	case prefixSession:
		return tvm.queries.DeleteSessionTokenByAccessHash(ctx, hash)
	case prefixAPIKey:
		return tvm.queries.DeleteAPITokenByHash(ctx, hash)
	default:
		return ErrInvalidExpiredToken
	}
}

// RevokeSession revokes a session by its ID. Intended for session management UI
// where the user sees a list of sessions and revokes one by ID.
func (tvm *VendingMachine) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	return tvm.queries.DeleteSessionToken(ctx, sessionID)
}

// Refresh validates a refresh token and issues a new access + refresh token pair,
// rotating the old refresh token. If the presented refresh token does not match
// the stored one (replay attack), the entire session is deleted.
func (tvm *VendingMachine) Refresh(ctx context.Context, refreshToken string) (accessToken string, newRefreshToken string, err error) {
	if tokenPrefix(refreshToken) != prefixRefresh {
		return "", "", ErrInvalidExpiredToken
	}

	hash := hashToken(refreshToken)
	session, err := tvm.queries.GetSessionByRefreshToken(ctx, hash)
	if err != nil {
		return "", "", ErrInvalidExpiredToken
	}

	// rotate: generate new access + refresh pair
	newAccess, newAccessHash := generateToken(prefixSession)
	newRefresh, newRefreshHash := generateToken(prefixRefresh)

	now := time.Now()
	if err := tvm.queries.RotateSessionToken(ctx, queries.RotateSessionTokenParams{
		ID:               session.ID,
		AccessTokenHash:  newAccessHash,
		RefreshTokenHash: newRefreshHash,
		AccessExpiresAt:  pgtype.Timestamptz{Time: now.Add(tvm.Cfg.SessionAccessTokenDuration), Valid: true},
		RefreshExpiresAt: pgtype.Timestamptz{Time: now.Add(tvm.Cfg.SessionRefreshTokenDuration), Valid: true},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to rotate session token", "err", err)
		return "", "", ErrStoreToken
	}

	return newAccess, newRefresh, nil
}

// ListSessions returns all active sessions for the user associated with the given session token.
func (tvm *VendingMachine) ListSessions(ctx context.Context, token string) ([]queries.ListSessionsForUserRow, error) {
	entity, _, err := tvm.GetToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if entity.Type != queries.EntityTypeUser {
		return nil, ErrImproperUsage
	}
	return tvm.queries.ListSessionsForUser(ctx, entity.ID)
}

// ListAPITokensForEntity lists all API tokens associated with the given entity.
// The caller is expected to have already verified permissions.
func (tvm *VendingMachine) ListAPITokensForEntity(ctx context.Context, entity queries.Entity) ([]queries.ListAPITokensForEntityRow, error) {
	return tvm.queries.ListAPITokensForEntity(ctx, queries.ListAPITokensForEntityParams{
		EntityType: entity.Type,
		EntityID:   entity.ID,
	})
}

// touchSessionLastUsed updates last_used_at on the session row, throttled by LastUsedUpdateInterval.
func (tvm *VendingMachine) touchSessionLastUsed(ctx context.Context, sessionID uuid.UUID, lastUsedAt pgtype.Timestamptz) {
	if lastUsedAt.Valid && time.Since(lastUsedAt.Time) < tvm.Cfg.LastUsedUpdateInterval {
		return
	}
	if err := tvm.queries.TouchSessionLastUsed(ctx, sessionID); err != nil {
		slog.ErrorContext(ctx, "failed to touch session last_used_at", "err", err)
	}
}

// touchAPITokenLastUsed updates last_used_at on the API token row, throttled by LastUsedUpdateInterval.
func (tvm *VendingMachine) touchAPITokenLastUsed(ctx context.Context, tokenID uuid.UUID, lastUsedAt pgtype.Timestamptz) {
	if lastUsedAt.Valid && time.Since(lastUsedAt.Time) < tvm.Cfg.LastUsedUpdateInterval {
		return
	}
	if err := tvm.queries.TouchAPITokenLastUsed(ctx, tokenID); err != nil {
		slog.ErrorContext(ctx, "failed to touch api token last_used_at", "err", err)
	}
}
