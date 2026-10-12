package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpc"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
)

const (
	readinessToken    = "readiness-probe"
	readinessEntityID = "00000000-0000-4000-8000-000000000000"
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

func NewValidator(controlPlaneURL string, authToken string, c cache.Cache) *Validator {
	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.Protocols.SetHTTP2(true)
	httpClient := &http.Client{Transport: transport}

	clients := rpc.NewHTTPClients(httpClient, controlPlaneURL, authToken)
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
		Token:      token,
		EntityType: entityType,
		EntityId:   entityID,
		Scope:      scope,
	}

	resp, err := v.client.CheckPermission(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "CheckPermission call failed", "error", err)
		if connect.CodeOf(err) == connect.CodePermissionDenied {
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

func (v *Validator) Ready(ctx context.Context) error {
	message := &tokenv1.CheckPermissionRequest{
		Token:      readinessToken,
		EntityType: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		EntityId:   readinessEntityID,
		Scope:      tokenv1.Scope_SCOPE_READ,
	}
	req := message
	if _, err := v.client.CheckPermission(ctx, req); err != nil {
		return fmt.Errorf("control plane permission endpoint: %w", err)
	}
	return nil
}
