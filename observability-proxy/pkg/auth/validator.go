package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	observabilityv1 "github.com/team-loco/loco/proto/loco/observability/v1"
	"github.com/team-loco/loco/proto/loco/observability/v1/observabilityv1connect"
)

// Validator validates observability tokens by calling the control plane.
type Validator struct {
	client    observabilityv1connect.ObservabilityAccessServiceClient
	authToken string // proxy auth token for calling ValidateObservabilityToken
	cache     *TokenCache
}

func NewValidator(controlPlaneURL string, authToken string, cacheTTL time.Duration) *Validator {
	httpClient := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
		},
		Timeout: 5 * time.Second,
	}

	client := observabilityv1connect.NewObservabilityAccessServiceClient(
		httpClient,
		controlPlaneURL,
	)

	return &Validator{
		client:    client,
		authToken: authToken,
		cache:     NewTokenCache(cacheTTL),
	}
}

// Validate checks a token against the control plane (with caching).
func (v *Validator) Validate(ctx context.Context, token string) (*ValidatedClaims, error) {
	// Check cache first
	if claims, ok := v.cache.Get(token); ok {
		return claims, nil
	}

	// Call control plane
	req := connect.NewRequest(&observabilityv1.ValidateObservabilityTokenRequest{
		Token: token,
	})
	req.Header().Set("Authorization", "Bearer "+v.authToken)

	resp, err := v.client.ValidateObservabilityToken(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "token validation failed", "error", err)
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	claims := &ValidatedClaims{
		WorkspaceID: resp.Msg.GetWorkspaceId(),
		ResourceIDs: resp.Msg.GetResourceIds(),
		Scopes:      resp.Msg.GetScopes(),
		ExpiresAt:   resp.Msg.GetExpiresAt().AsTime(),
	}

	// Check if token is already expired
	if time.Now().After(claims.ExpiresAt) {
		return nil, fmt.Errorf("token expired")
	}

	// Cache the result
	v.cache.Set(token, claims)
	return claims, nil
}
