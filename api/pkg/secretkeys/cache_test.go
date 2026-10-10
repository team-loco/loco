package secretkeys

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const testCacheTTL = time.Minute

var errProviderDown = errors.New("provider unavailable")

type countingProvider struct {
	Provider

	mu      sync.Mutex
	unwraps int
	down    bool
}

func (p *countingProvider) Unwrap(ctx context.Context, wrapped WrappedKey, aad []byte) ([]byte, error) {
	p.mu.Lock()
	p.unwraps++
	down := p.down
	p.mu.Unlock()
	if down {
		return nil, errProviderDown
	}
	return p.Provider.Unwrap(ctx, wrapped, aad)
}

func (p *countingProvider) setDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

func (p *countingProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unwraps
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestCache(t *testing.T) (*DEKCache, *countingProvider, *testClock) {
	t.Helper()
	inner := &countingProvider{Provider: localProvider(t, "k1:"+testKey(t, 1))}
	clock := &testClock{now: time.Unix(0, 0)}
	cache := NewDEKCache(inner, testCacheTTL)
	cache.now = clock.Now
	return cache, inner, clock
}

func wrapForCache(t *testing.T, provider Provider, aad []byte) ([]byte, WrappedKey) {
	t.Helper()
	dek := testDEK(t)
	wrapped, err := provider.Wrap(context.Background(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	return dek, wrapped
}

func mustUnwrap(t *testing.T, provider Provider, wrapped WrappedKey, aad []byte) []byte {
	t.Helper()
	dek, err := provider.Unwrap(context.Background(), wrapped, aad)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	return dek
}

func TestDEKCacheHitSkipsTheProvider(t *testing.T) {
	cache, inner, _ := newTestCache(t)
	aad := DEKAAD(environmentID)
	dek, wrapped := wrapForCache(t, inner.Provider, aad)

	first := mustUnwrap(t, cache, wrapped, aad)
	Zero(first)
	second := mustUnwrap(t, cache, wrapped, aad)
	if !bytes.Equal(second, dek) {
		t.Fatal("cached key differs from the original after the caller zeroed its copy")
	}
	if calls := inner.calls(); calls != 1 {
		t.Fatalf("provider unwraps = %d, want 1", calls)
	}
}

func TestDEKCacheServesUnwrapsDuringAnOutage(t *testing.T) {
	cache, inner, clock := newTestCache(t)
	aad := DEKAAD(environmentID)
	dek, wrapped := wrapForCache(t, inner.Provider, aad)
	mustUnwrap(t, cache, wrapped, aad)

	inner.setDown(true)
	if got := mustUnwrap(t, cache, wrapped, aad); !bytes.Equal(got, dek) {
		t.Fatal("cached key differs from the original")
	}
	clock.advance(testCacheTTL)
	if _, err := cache.Unwrap(context.Background(), wrapped, aad); !errors.Is(err, errProviderDown) {
		t.Fatalf("unwrap after expiry during an outage: err = %v, want the provider's error", err)
	}
}

func TestDEKCacheExpiryZeroesTheEntry(t *testing.T) {
	cache, inner, clock := newTestCache(t)
	aad := DEKAAD(environmentID)
	dek, wrapped := wrapForCache(t, inner.Provider, aad)
	mustUnwrap(t, cache, wrapped, aad)
	cached := cache.entries[string(aad)].dek

	clock.advance(testCacheTTL)
	got := mustUnwrap(t, cache, wrapped, aad)
	if !bytes.Equal(got, dek) {
		t.Fatal("unwrapped key differs from the original")
	}
	if calls := inner.calls(); calls != 2 {
		t.Fatalf("provider unwraps = %d, want 2", calls)
	}
	if !bytes.Equal(cached, make([]byte, KeySize)) {
		t.Fatal("expired entry was not zeroed")
	}
}

func TestDEKCacheInvalidatesARewrappedKey(t *testing.T) {
	cache, inner, _ := newTestCache(t)
	aad := DEKAAD(environmentID)
	dek, wrapped := wrapForCache(t, inner.Provider, aad)
	mustUnwrap(t, cache, wrapped, aad)
	cached := cache.entries[string(aad)].dek

	rewrapped, err := inner.Wrap(context.Background(), dek, aad)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	mustUnwrap(t, cache, rewrapped, aad)
	if calls := inner.calls(); calls != 2 {
		t.Fatalf("provider unwraps = %d, want 2 after the wrapped key changed", calls)
	}
	if !bytes.Equal(cached, make([]byte, KeySize)) {
		t.Fatal("entry for the old wrapped key was not zeroed")
	}
	inner.setDown(true)
	if _, err := cache.Unwrap(context.Background(), wrapped, aad); !errors.Is(err, errProviderDown) {
		t.Fatalf("unwrap of the old wrapped key: err = %v, want a provider call", err)
	}
}

func TestDEKCacheIsKeyedByEnvironment(t *testing.T) {
	cache, inner, _ := newTestCache(t)
	aad := DEKAAD(environmentID)
	_, wrapped := wrapForCache(t, inner.Provider, aad)
	mustUnwrap(t, cache, wrapped, aad)

	other := DEKAAD(otherEnvironmentID)
	if _, err := cache.Unwrap(context.Background(), wrapped, other); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("unwrap with another environment's aad: err = %v, want ErrDecrypt", err)
	}
}

func TestDEKCacheWrapStoresTheNewKey(t *testing.T) {
	cache, inner, _ := newTestCache(t)
	aad := DEKAAD(environmentID)
	dek := testDEK(t)
	wrapped, err := cache.Wrap(context.Background(), dek, aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	Zero(dek)
	inner.setDown(true)
	got := mustUnwrap(t, cache, wrapped, aad)
	if bytes.Equal(got, make([]byte, KeySize)) {
		t.Fatal("cache shares its key with the caller of Wrap")
	}
	if calls := inner.calls(); calls != 0 {
		t.Fatalf("provider unwraps = %d, want none after Wrap", calls)
	}
}

func TestDEKCacheEvictsExpiredEntriesOfOtherEnvironments(t *testing.T) {
	cache, inner, clock := newTestCache(t)
	aad := DEKAAD(environmentID)
	_, wrapped := wrapForCache(t, inner.Provider, aad)
	mustUnwrap(t, cache, wrapped, aad)
	stale := cache.entries[string(aad)].dek

	clock.advance(testCacheTTL)
	otherAAD := DEKAAD(otherEnvironmentID)
	_, otherWrapped := wrapForCache(t, inner.Provider, otherAAD)
	mustUnwrap(t, cache, otherWrapped, otherAAD)
	if _, ok := cache.entries[string(aad)]; ok {
		t.Fatal("expired entry is still cached")
	}
	if !bytes.Equal(stale, make([]byte, KeySize)) {
		t.Fatal("expired entry was not zeroed")
	}
}

func TestNewCachesTransitUnwraps(t *testing.T) {
	fake := newFakeTransit(t)
	server := fake.server()
	cfg := transitConfig(server.URL)
	provider, err := New(t.Context(), Config{Provider: ProviderTransit, Transit: cfg})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	aad := DEKAAD(environmentID)
	wrapped, err := provider.Wrap(context.Background(), testDEK(t), aad)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	mustUnwrap(t, provider, wrapped, aad)
	if calls := fake.count("/v1/transit/decrypt/loco"); calls != 0 {
		t.Fatalf("decrypt calls = %d, want none for a key the provider wrapped", calls)
	}
}
