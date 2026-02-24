package auth

import (
	"sync"
	"time"
)

// ValidatedClaims holds the result of a successful token validation.
type ValidatedClaims struct {
	WorkspaceID string
	ResourceIDs []string
	Scopes      []string
	ExpiresAt   time.Time
}

type cacheEntry struct {
	claims   *ValidatedClaims
	cachedAt time.Time
}

// TokenCache is a simple in-memory TTL cache for validated tokens.
type TokenCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

func NewTokenCache(ttl time.Duration) *TokenCache {
	tc := &TokenCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
	go tc.cleanup()
	return tc
}

func (c *TokenCache) Get(token string) (*ValidatedClaims, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[token]
	if !ok {
		return nil, false
	}
	if time.Since(entry.cachedAt) > c.ttl {
		return nil, false
	}
	return entry.claims, true
}

func (c *TokenCache) Set(token string, claims *ValidatedClaims) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[token] = cacheEntry{
		claims:   claims,
		cachedAt: time.Now(),
	}
}

// cleanup runs every minute and removes expired entries.
func (c *TokenCache) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.entries {
			if now.Sub(v.cachedAt) > c.ttl {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
