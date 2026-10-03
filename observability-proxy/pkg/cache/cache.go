package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
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

const (
	memoryMaxCost     = 32 << 20
	memoryNumCounters = 1 << 17
	memoryBufferItems = 64
)

type MemoryCache struct {
	cache      *ristretto.Cache[string, []byte]
	defaultTTL time.Duration
}

func NewMemory(defaultTTL time.Duration) (*MemoryCache, error) {
	c, err := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: memoryNumCounters,
		MaxCost:     memoryMaxCost,
		BufferItems: memoryBufferItems,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory cache: %w", err)
	}
	return &MemoryCache{cache: c, defaultTTL: defaultTTL}, nil
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := m.cache.Get(key)
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out, nil
}

func (m *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl == 0 {
		ttl = m.defaultTTL
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	cost := int64(len(key) + len(stored))
	m.cache.SetWithTTL(key, stored, cost, ttl)
	return nil
}

func (m *MemoryCache) Close() error {
	m.cache.Close()
	return nil
}
