package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
	tokenv1 "github.com/team-loco/loco/proto/loco/token/v1"
	"github.com/team-loco/loco/proto/loco/token/v1/tokenv1connect"
	"golang.org/x/net/http2"
)

// Validator checks token permissions by calling CheckPermission on the control plane.
type Validator struct {
	client    tokenv1connect.TokenServiceClient
	authToken string // proxy auth token for calling the control plane
	cache     cache.Cache
}

func NewValidator(controlPlaneURL string, authToken string, c cache.Cache) *Validator {
	transport := &http.Transport{}
	err := http2.ConfigureTransport(transport)
	if err != nil {
		panic("failed to configure HTTP/2 transport: " + err.Error())
	}
	httpClient := &http.Client{Transport: transport}

	client := tokenv1connect.NewTokenServiceClient(
		httpClient,
		controlPlaneURL,
	)

	return &Validator{
		client:    client,
		authToken: authToken,
		cache:     c,
	}
}

// CheckPermission validates whether the given token has the requested permission.
// Results are cached for the cache's configured TTL.
func (v *Validator) CheckPermission(ctx context.Context, token string, entityType tokenv1.EntityType, entityID string, scope tokenv1.Scope) error {
	cacheKey := token + ":" + entityType.String() + ":" + entityID + ":" + scope.String()

	if allowed, ok := getPermission(ctx, v.cache, cacheKey); ok {
		if !allowed {
			return fmt.Errorf("permission denied")
		}
		return nil
	}

	req := connect.NewRequest(&tokenv1.CheckPermissionRequest{
		Token:      token,
		EntityType: entityType,
		EntityId:   entityID,
		Scope:      scope,
	})
	req.Header().Set("Authorization", "Bearer "+v.authToken)

	resp, err := v.client.CheckPermission(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "CheckPermission call failed", "error", err)
		return fmt.Errorf("permission check failed: %w", err)
	}

	setPermission(ctx, v.cache, cacheKey, resp.Msg.GetAllowed())

	if !resp.Msg.GetAllowed() {
		return fmt.Errorf("permission denied")
	}
	return nil
}
