package tvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	queries "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/internal/bearer"
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
func (tvm *VendingMachine) GetToken(ctx context.Context, token string) (reqctx.Principal, error) {
	hash := bearer.Hash(token)

	switch tokenPrefix(token) {
	case prefixSession:
		session, err := tvm.queries.GetSessionWithScopesByAccessToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return reqctx.Principal{}, ErrInvalidExpiredToken
		}
		if err != nil {
			return reqctx.Principal{}, fmt.Errorf("get session: %w", err)
		}

		entity := queries.Entity{
			Type: queries.EntityTypeUser,
			ID:   session.UserID,
		}

		var liveScopes []queries.EntityScope
		if err := json.Unmarshal(session.Scopes, &liveScopes); err != nil {
			return reqctx.Principal{}, fmt.Errorf("decode session scopes: %w", err)
		}

		tvm.touchSessionLastUsed(ctx, session.ID, session.LastUsedAt)

		return reqctx.Principal{
			Entity:    entity,
			Scopes:    liveScopes,
			Token:     token,
			ActorType: string(queries.EntityTypeUser),
			ActorID:   session.UserID,
			Method:    queries.DeploymentMethodCli,
		}, nil

	case prefixAPIKey:
		tok, err := tvm.queries.GetAPIToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return reqctx.Principal{}, ErrInvalidExpiredToken
		}
		if err != nil {
			return reqctx.Principal{}, fmt.Errorf("get api token: %w", err)
		}

		entity := queries.Entity{
			Type: tok.EntityType,
			ID:   tok.EntityID,
		}

		tvm.touchAPITokenLastUsed(ctx, tok.ID, tok.LastUsedAt)

		return reqctx.Principal{
			Entity:    entity,
			Scopes:    tok.Scopes,
			Token:     token,
			ActorType: "api_token",
			ActorID:   tok.ID,
			Method:    queries.DeploymentMethodApiToken,
		}, nil

	default:
		return reqctx.Principal{}, ErrInvalidExpiredToken
	}
}

func IsLocoToken(token string) bool {
	return tokenPrefix(token) != ""
}

// Revoke immediately invalidates the given token.
func (tvm *VendingMachine) Revoke(ctx context.Context, token string) error {
	hash := bearer.Hash(token)
	switch tokenPrefix(token) {
	case prefixSession:
		return tvm.queries.DeleteSessionTokenByAccessHash(ctx, hash)
	case prefixAPIKey:
		return tvm.queries.DeleteAPITokenByHash(ctx, hash)
	default:
		return ErrInvalidExpiredToken
	}
}

// Refresh validates a refresh token and issues a new access + refresh token pair,
// rotating the old refresh token. If the presented refresh token does not match
// the stored one (replay attack), the entire session is deleted.
func (tvm *VendingMachine) Refresh(
	ctx context.Context,
	refreshToken string,
) (accessToken string, newRefreshToken string, err error) {
	if tokenPrefix(refreshToken) != prefixRefresh {
		return "", "", ErrInvalidExpiredToken
	}

	hash := bearer.Hash(refreshToken)
	session, err := tvm.queries.GetSessionByRefreshToken(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrInvalidExpiredToken
	}
	if err != nil {
		return "", "", fmt.Errorf("get session by refresh token: %w", err)
	}

	// rotate: generate new access + refresh pair
	newAccess, newAccessHash := generateToken(prefixSession)
	newRefresh, newRefreshHash := generateToken(prefixRefresh)

	now := time.Now()
	rotated, err := tvm.queries.RotateSessionToken(ctx, queries.RotateSessionTokenParams{
		ID:                  session.ID,
		OldRefreshTokenHash: hash,
		AccessTokenHash:     newAccessHash,
		RefreshTokenHash:    newRefreshHash,
		AccessExpiresAt:     now.Add(tvm.Cfg.SessionAccessTokenDuration),
		RefreshExpiresAt:    now.Add(tvm.Cfg.SessionRefreshTokenDuration),
	})
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrStoreToken, err)
	}
	if rotated == 0 {
		slog.WarnContext(ctx, "refresh token reused, revoking session", "sessionId", session.ID)
		if deleteErr := tvm.queries.DeleteSessionToken(ctx, session.ID); deleteErr != nil {
			slog.ErrorContext(ctx, "failed to revoke session after refresh token reuse", "err", deleteErr)
		}
		return "", "", ErrInvalidExpiredToken
	}

	return newAccess, newRefresh, nil
}

// ListAPITokensForEntity lists all API tokens associated with the given entity.
// The caller is expected to have already verified permissions.
func (tvm *VendingMachine) ListAPITokensForEntity(
	ctx context.Context,
	entity queries.Entity,
) ([]queries.ListAPITokensForEntityRow, error) {
	return tvm.queries.ListAPITokensForEntity(ctx, queries.ListAPITokensForEntityParams{
		EntityType: entity.Type,
		EntityID:   entity.ID,
	})
}

// touchSessionLastUsed updates last_used_at on the session row, throttled by LastUsedUpdateInterval.
func (tvm *VendingMachine) touchSessionLastUsed(ctx context.Context, sessionID uuid.UUID, lastUsedAt time.Time) {
	if time.Since(lastUsedAt) < tvm.Cfg.LastUsedUpdateInterval {
		return
	}
	if err := tvm.queries.TouchSessionLastUsed(ctx, sessionID); err != nil {
		slog.ErrorContext(ctx, "failed to touch session last_used_at", "err", err)
	}
}

// touchAPITokenLastUsed updates last_used_at on the API token row, throttled by LastUsedUpdateInterval.
func (tvm *VendingMachine) touchAPITokenLastUsed(ctx context.Context, tokenID uuid.UUID, lastUsedAt *time.Time) {
	if lastUsedAt != nil && time.Since(*lastUsedAt) < tvm.Cfg.LastUsedUpdateInterval {
		return
	}
	if err := tvm.queries.TouchAPITokenLastUsed(ctx, tokenID); err != nil {
		slog.ErrorContext(ctx, "failed to touch api token last_used_at", "err", err)
	}
}

func (tvm *VendingMachine) SessionIdentity(
	ctx context.Context,
	refreshToken string,
) (queries.GetSessionIdentityByRefreshHashRow, error) {
	return tvm.queries.GetSessionIdentityByRefreshHash(ctx, bearer.Hash(refreshToken))
}

func (tvm *VendingMachine) RevokeIdentitySessions(ctx context.Context, identityID uuid.UUID) error {
	return tvm.queries.DeleteSessionTokensForIdentity(ctx, &identityID)
}
