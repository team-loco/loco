package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/valkeyotel"
)

// ErrNotFound is returned when a key is not found in the cache
var ErrNotFound = errors.New("cache: key not found")

// Cache defines the interface for key-value caching operations
type Cache interface {
	// Get retrieves a value by key. Returns ErrNotFound if key doesn't exist.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores a value with the given TTL.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error

	// SetIfAbsent stores a value only if the key does not exist, reporting whether it was stored.
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)

	// Take removes a key, reporting whether it existed.
	Take(ctx context.Context, key string) (bool, error)

	// Close releases any resources held by the cache.
	Close() error
}

var ErrNotStored = errors.New("cache: value was not stored")

const (
	memoryMaxCost     = 64 << 20
	memoryNumCounters = 1 << 16
	memoryBufferItems = 64
)

type MemoryCache struct {
	mu    sync.Mutex
	cache *ristretto.Cache[string, []byte]
}

func NewMemory() (*MemoryCache, error) {
	c, err := ristretto.NewCache(&ristretto.Config[string, []byte]{
		NumCounters: memoryNumCounters,
		MaxCost:     memoryMaxCost,
		BufferItems: memoryBufferItems,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory cache: %w", err)
	}
	return &MemoryCache{cache: c}, nil
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
	stored := make([]byte, len(value))
	copy(stored, value)
	cost := int64(len(key) + len(stored))
	if !m.cache.SetWithTTL(key, stored, cost, ttl) {
		return ErrNotStored
	}
	m.cache.Wait()
	if _, ok := m.cache.Get(key); !ok {
		return ErrNotStored
	}
	return nil
}

func (m *MemoryCache) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cache.Get(key); ok {
		return false, nil
	}
	if err := m.Set(ctx, key, value, ttl); err != nil {
		return false, err
	}
	return true, nil
}

func (m *MemoryCache) Take(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cache.Get(key); !ok {
		return false, nil
	}
	m.cache.Del(key)
	return true, nil
}

func (m *MemoryCache) Close() error {
	m.cache.Close()
	return nil
}

// ValkeyAdapter wraps valkey-go client
type ValkeyAdapter struct {
	client valkey.Client
}

func NewValkey(cacheAddr string) (*ValkeyAdapter, error) {
	clientOpts, parseErr := valkey.ParseURL(cacheAddr)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to valkey URL: %w", parseErr)
	}
	client, err := valkeyotel.NewClient(clientOpts)
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

	return &ValkeyAdapter{client: client}, nil
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
	return v.client.Do(ctx, v.client.B().Set().Key(key).Value(string(value)).Ex(ttl).Build()).Error()
}

func (v *ValkeyAdapter) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	cmd := v.client.B().Set().Key(key).Value(string(value)).Nx().Ex(ttl).Build()
	err := v.client.Do(ctx, cmd).Error()
	if valkey.IsValkeyNil(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (v *ValkeyAdapter) Take(ctx context.Context, key string) (bool, error) {
	cmd := v.client.B().Getdel().Key(key).Build()
	err := v.client.Do(ctx, cmd).Error()
	if valkey.IsValkeyNil(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (v *ValkeyAdapter) Close() error {
	v.client.Close()
	return nil
}
