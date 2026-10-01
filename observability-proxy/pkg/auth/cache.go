package auth

import (
	"context"
	"errors"
	"log/slog"

	"github.com/team-loco/loco/observability-proxy/pkg/cache"
)

// permissionCacheAllowed is the value stored when permission is granted.
var permissionCacheAllowed = []byte("1")

// setPermission stores a permission result in the cache.
func setPermission(ctx context.Context, c cache.Cache, key string, allowed bool) {
	val := []byte("0")
	if allowed {
		val = permissionCacheAllowed
	}
	if err := c.Set(ctx, key, val, 0); err != nil {
		slog.WarnContext(ctx, "failed to cache permission result", "key", key, "error", err)
	}
}

// getPermission retrieves a cached permission result.
// Returns (allowed, found).
func getPermission(ctx context.Context, c cache.Cache, key string) (bool, bool) {
	val, err := c.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return false, false
	}
	if err != nil {
		return false, false
	}
	return string(val) == "1", true
}
