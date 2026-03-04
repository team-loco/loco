package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/valkey-io/valkey-go"
)

// ErrNotFound is returned when a key is not found in the cache
var ErrNotFound = errors.New("cache: key not found")

// Cache defines the interface for key-value caching operations
type Cache interface {
	// Get retrieves a value by key. Returns ErrNotFound if key doesn't exist.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value with the given TTL. TTL of 0 means use default.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// Delete removes a key. No error if key doesn't exist.
	Delete(ctx context.Context, key string) error

	// Close releases any resources held by the cache.
	Close() error
}

// BigCacheAdapter wraps bigcache for in-memory caching
type BigCacheAdapter struct {
	cache *bigcache.BigCache
}

func NewBigCache(defaultTTL time.Duration) (*BigCacheAdapter, error) {
	config := bigcache.DefaultConfig(defaultTTL)
	bc, err := bigcache.New(context.Background(), config)
	if err != nil {
		return nil, err
	}
	return &BigCacheAdapter{cache: bc}, nil
}

func (b *BigCacheAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := b.cache.Get(key)
	if errors.Is(err, bigcache.ErrEntryNotFound) {
		return nil, ErrNotFound
	}
	return val, err
}

func (b *BigCacheAdapter) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	// no per item ttl
	return b.cache.Set(key, value)
}

func (b *BigCacheAdapter) Delete(ctx context.Context, key string) error {
	return b.cache.Delete(key)
}

func (b *BigCacheAdapter) Close() error {
	return b.cache.Close()
}

// ValkeyAdapter wraps valkey-go client
type ValkeyAdapter struct {
	client     valkey.Client
	defaultTTL time.Duration
}

func NewValkey(CacheAddr string, defaultTTL time.Duration) (*ValkeyAdapter, error) {
	clientOpts, parseErr := valkey.ParseURL(CacheAddr)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to valkey URL: %w", parseErr)
	}
	client, err := valkey.NewClient(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to valkey: %w", err)
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to ping valkey: %w", err)
	}

	return &ValkeyAdapter{client: client, defaultTTL: defaultTTL}, nil
}

func (v *ValkeyAdapter) Get(ctx context.Context, key string) ([]byte, error) {
	resp := v.client.Do(ctx, v.client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return resp.AsBytes()
}

func (v *ValkeyAdapter) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl == 0 {
		ttl = v.defaultTTL
	}
	return v.client.Do(ctx, v.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()).Error()
}

func (v *ValkeyAdapter) Delete(ctx context.Context, key string) error {
	return v.client.Do(ctx, v.client.B().Del().Key(key).Build()).Error()
}

func (v *ValkeyAdapter) Close() error {
	v.client.Close()
	return nil
}
