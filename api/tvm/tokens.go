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
// Personal API tokens are bounded by live user grants; owned tokens use their stored scopes.
//
// Refresh tokens (loco_r_...) are rejected — they are not valid access credentials.
func (tvm *VendingMachine) GetToken(ctx context.Context, token string) (reqctx.Principal, error) {
	return tvm.getToken(ctx, token, true)
}

// ValidateToken reads current credential validity and permissions without writing
// last-used metadata. Long-lived streams use it while rechecking access.
func (tvm *VendingMachine) ValidateToken(ctx context.Context, token string) (reqctx.Principal, error) {
	return tvm.getToken(ctx, token, false)
}

func (tvm *VendingMachine) getToken(ctx context.Context, token string, touchLastUsed bool) (reqctx.Principal, error) {
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

		if touchLastUsed {
			tvm.touchSessionLastUsed(ctx, session.ID, session.LastUsedAt)
		}

		method := queries.DeploymentMethodCli
		identityID := uuid.Nil
		if session.Kind == queries.SessionKindWeb {
			method = queries.DeploymentMethodWeb
			identityID = *session.IdentityID
		}
		return reqctx.Principal{
			Entity:     entity,
			Scopes:     liveScopes,
			Token:      token,
			ActorType:  queries.DeploymentActorTypeUser,
			ActorID:    session.UserID,
			UserID:     session.UserID,
			Method:     method,
			IdentityID: identityID,
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

		scopes := tok.Scopes
		userID := uuid.Nil
		if tok.PersonalUserID != nil {
			userID = *tok.PersonalUserID
			var live []queries.EntityScope
			if decodeErr := json.Unmarshal(tok.LiveScopes, &live); decodeErr != nil {
				return reqctx.Principal{}, fmt.Errorf("decode personal token grants: %w", decodeErr)
			}
			scopes, err = tvm.authz.Intersect(ctx, scopes, live)
			if err != nil {
				return reqctx.Principal{}, fmt.Errorf("bound personal token permissions: %w", err)
			}
		}
		if touchLastUsed {
			tvm.touchAPITokenLastUsed(ctx, tok.ID, tok.LastUsedAt)
		}

		return reqctx.Principal{
			Entity:    entity,
			Scopes:    scopes,
			Token:     token,
			ActorType: queries.DeploymentActorTypeApiToken,
			ActorID:   tok.ID,
			UserID:    userID,
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

// RevokeWebSession ends a web session identified by either browser cookie.
func (tvm *VendingMachine) RevokeWebSession(ctx context.Context, token string) error {
	hash := bearer.Hash(token)
	switch tokenPrefix(token) {
	case prefixSession:
		return tvm.queries.DeleteWebSessionByAccessHash(ctx, hash)
	case prefixRefresh:
		session, err := tvm.queries.GetSessionByRefreshToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get web session to revoke: %w", err)
		}
		if session.Kind == queries.SessionKindWeb {
			return tvm.queries.DeleteSessionToken(ctx, session.ID)
		}
	}
	return nil
}

// Refresh validates a refresh token and issues a new access + refresh token pair,
// rotating the old refresh token. Reuse of a consumed token revokes the session.
// Callers running in a transaction must commit that revocation even on rejection.
func (tvm *VendingMachine) Refresh(
	ctx context.Context,
	refreshToken string,
	kind queries.SessionKind,
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

	if session.Kind != kind {
		return "", "", ErrInvalidExpiredToken
	}
	if session.Used {
		return "", "", tvm.revokeReusedSession(ctx, session.ID)
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
		return "", "", tvm.revokeReusedSession(ctx, session.ID)
	}

	return newAccess, newRefresh, nil
}

func (tvm *VendingMachine) revokeReusedSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := tvm.queries.DeleteSessionToken(ctx, sessionID); err != nil {
		return fmt.Errorf("revoke session after refresh token reuse: %w", err)
	}
	slog.WarnContext(ctx, "refresh token reused, revoking session", "sessionId", sessionID)
	return ErrInvalidExpiredToken
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
