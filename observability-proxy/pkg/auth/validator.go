package auth

import (
	"context"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpc"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
)

var (
	errPermissionDenied           = connect.NewError(connect.CodePermissionDenied, "permission denied")
	errPermissionCheckUnavailable = connect.NewError(connect.CodeUnavailable, "permission check unavailable")
)

// Validator checks token permissions by calling CheckPermission on the control plane.
type Validator struct {
	client tokenv1connect.TokenServiceClient
	cache  cache.Cache
}

func NewValidator(controlPlaneURL string, c cache.Cache) *Validator {
	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.Protocols.SetHTTP2(true)
	httpClient := &http.Client{Transport: transport}

	clients := rpc.NewHTTPClients(httpClient, controlPlaneURL, "")
	tokenClient := clients.Token

	return &Validator{
		client: tokenClient,
		cache:  c,
	}
}

// CheckPermission validates whether the given token has the requested permission.
// Results are cached for the cache's configured TTL.
func (v *Validator) CheckPermission(
	ctx context.Context,
	token string,
	entityType tokenv1.EntityType,
	entityID string,
	scope tokenv1.Scope,
) error {
	cacheKey := token + ":" + entityType.String() + ":" + entityID + ":" + scope.String()

	if allowed, ok := getPermission(ctx, v.cache, cacheKey); ok {
		if !allowed {
			return errPermissionDenied
		}
		return nil
	}

	req := &tokenv1.CheckPermissionRequest{
		EntityType: entityType,
		EntityId:   entityID,
		Scope:      scope,
	}

	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	resp, err := v.client.CheckPermission(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "CheckPermission call failed", "error", err)
		code := connect.CodeOf(err)
		if code == connect.CodePermissionDenied || code == connect.CodeUnauthenticated {
			return errPermissionDenied.WithCause(err)
		}
		return errPermissionCheckUnavailable.WithCause(err)
	}

	setPermission(ctx, v.cache, cacheKey, resp.GetAllowed())

	if !resp.GetAllowed() {
		return errPermissionDenied
	}
	return nil
}
