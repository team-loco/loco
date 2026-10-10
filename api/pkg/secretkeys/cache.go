package secretkeys

import (
	"bytes"
	"context"
	"sync"
	"time"
)

type dekCacheEntry struct {
	wrapped   WrappedKey
	dek       []byte
	expiresAt time.Time
}

// DEKCache wraps a Provider and keeps each unwrapped data key in memory for a fixed TTL,
// so unwraps keep working through a short provider outage. Entries are keyed by the
// data key's additional data, which names its environment, and hit only for the exact
// wrapped key that was unwrapped or wrapped, so a rewrap or a new data key misses.
// Callers receive copies; evicted entries are zeroed.
type DEKCache struct {
	Provider

	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*dekCacheEntry
}

// NewDEKCache returns provider with unwrapped data keys cached for ttl.
func NewDEKCache(provider Provider, ttl time.Duration) *DEKCache {
	return &DEKCache{
		Provider: provider,
		ttl:      ttl,
		now:      time.Now,
		entries:  map[string]*dekCacheEntry{},
	}
}

func (c *DEKCache) Wrap(ctx context.Context, dek, aad []byte) (WrappedKey, error) {
	wrapped, err := c.Provider.Wrap(ctx, dek, aad)
	if err != nil {
		return WrappedKey{}, err
	}
	c.store(aad, wrapped, dek)
	return wrapped, nil
}

func (c *DEKCache) Unwrap(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	if dek, ok := c.lookup(aad, wrapped); ok {
		return dek, nil
	}
	dek, err := c.Provider.Unwrap(ctx, wrapped, aad)
	if err != nil {
		return nil, err
	}
	c.store(aad, wrapped, dek)
	return dek, nil
}

func (c *DEKCache) lookup(aad []byte, wrapped WrappedKey) ([]byte, bool) {
	key := string(aad)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !c.now().Before(entry.expiresAt) || !sameWrappedKey(entry.wrapped, wrapped) {
		c.evict(key)
		return nil, false
	}
	return bytes.Clone(entry.dek), true
}

func (c *DEKCache) store(aad []byte, wrapped WrappedKey, dek []byte) {
	key := string(aad)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for existing, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			c.evict(existing)
		}
	}
	c.evict(key)
	c.entries[key] = &dekCacheEntry{
		wrapped: WrappedKey{
			Provider: wrapped.Provider,
			KeyID:    wrapped.KeyID,
			Bytes:    bytes.Clone(wrapped.Bytes),
		},
		dek:       bytes.Clone(dek),
		expiresAt: now.Add(c.ttl),
	}
}

func (c *DEKCache) evict(key string) {
	entry, ok := c.entries[key]
	if !ok {
		return
	}
	Zero(entry.dek)
	delete(c.entries, key)
}

func sameWrappedKey(a, b WrappedKey) bool {
	return a.Provider == b.Provider && a.KeyID == b.KeyID && bytes.Equal(a.Bytes, b.Bytes)
}
