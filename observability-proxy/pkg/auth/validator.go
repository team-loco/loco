package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/dgraph-io/ristretto/v2"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
)

var (
	// ErrPermissionDenied means the control plane refused the caller or rejected its token.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrPermissionCheckUnavailable means the control plane could not answer the check.
	ErrPermissionCheckUnavailable = errors.New("permission check unavailable")
)

const (
	cacheMaxCost     = 32 << 20
	cacheNumCounters = 1 << 17
	cacheBufferItems = 64
)

// Validator checks token permissions by calling CheckPermission on the control plane.
type Validator struct {
	client tokenv1connect.TokenServiceClient
	cache  *ristretto.Cache[string, bool]
	ttl    time.Duration
}

func NewValidator(controlPlaneURL string, cacheTTL time.Duration) (*Validator, error) {
	cache, err := ristretto.NewCache(&ristretto.Config[string, bool]{
		NumCounters: cacheNumCounters,
		MaxCost:     cacheMaxCost,
		BufferItems: cacheBufferItems,
	})
	if err != nil {
		return nil, fmt.Errorf("create permission cache: %w", err)
	}

	transport := &http.Transport{}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.Protocols.SetHTTP2(true)
	httpClient := &http.Client{Transport: transport}

	rpcTransport := connecthttp.NewTransport(httpClient, controlPlaneURL)
	tokenClient := tokenv1connect.NewTokenServiceClient(connect.NewClient(rpcTransport, rpc.RequestIDInterceptor))
	return &Validator{client: tokenClient, cache: cache, ttl: cacheTTL}, nil
}

// Close releases the permission cache.
func (v *Validator) Close() {
	v.cache.Close()
}

// CheckPermission validates whether the given token has the requested permission. It returns
// ErrPermissionDenied or ErrPermissionCheckUnavailable, wrapping the control plane's error, and
// does not log: the caller decides what to report. Results are cached for the validator's TTL.
func (v *Validator) CheckPermission(
	ctx context.Context,
	token string,
	entityType tokenv1.EntityType,
	entityID string,
	scope tokenv1.Scope,
) error {
	cacheKey := token + ":" + entityType.String() + ":" + entityID + ":" + scope.String()

	if allowed, ok := v.cache.Get(cacheKey); ok {
		if !allowed {
			return ErrPermissionDenied
		}
		return nil
	}

	req := &tokenv1.CheckPermissionRequest{
		EntityType: entityType,
		EntityId:   entityID,
		Scope:      scope,
	}

	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", bearer.Header(token))
	resp, err := v.client.CheckPermission(ctx, req)
	if err != nil {
		code := connect.CodeOf(err)
		if code == connect.CodePermissionDenied || code == connect.CodeUnauthenticated {
			return fmt.Errorf("%w: %w", ErrPermissionDenied, err)
		}
		return fmt.Errorf("%w: %w", ErrPermissionCheckUnavailable, err)
	}

	v.cache.SetWithTTL(cacheKey, resp.GetAllowed(), int64(len(cacheKey)+1), v.ttl)

	if !resp.GetAllowed() {
		return ErrPermissionDenied
	}
	return nil
}
