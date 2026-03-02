package cache

import (
	"context"
	"errors"
	"time"

	"github.com/allegro/bigcache/v3"
)

// ErrNotFound is returned when a key is not found in the cache.
var ErrNotFound = errors.New("cache: key not found")

// Cache defines the interface for key-value caching operations.
type Cache interface {
	// Get retrieves a value by key. Returns ErrNotFound if key doesn't exist.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value with the given TTL. TTL of 0 means use the cache default.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Close releases any resources held by the cache.
	Close() error
}

// BigCacheAdapter wraps bigcache for in-memory caching.
type BigCacheAdapter struct {
	cache *bigcache.BigCache
}

func NewBigCache(defaultTTL time.Duration) (*BigCacheAdapter, error) {
	bc, err := bigcache.New(context.Background(), bigcache.DefaultConfig(defaultTTL))
	if err != nil {
		return nil, err
	}
	return &BigCacheAdapter{cache: bc}, nil
}

func (b *BigCacheAdapter) Get(_ context.Context, key string) ([]byte, error) {
	val, err := b.cache.Get(key)
	if errors.Is(err, bigcache.ErrEntryNotFound) {
		return nil, ErrNotFound
	}
	return val, err
}

func (b *BigCacheAdapter) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	// bigcache does not support per-item TTL; TTL is set at cache creation time.
	return b.cache.Set(key, value)
}

func (b *BigCacheAdapter) Close() error {
	return b.cache.Close()
}
