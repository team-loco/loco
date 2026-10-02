package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestMemory(t *testing.T) *MemoryCache {
	t.Helper()
	c, err := NewMemory(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeErr := c.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})
	return c
}

func TestMemoryCacheHonoursPerEntryTTL(t *testing.T) {
	ctx := context.Background()
	c := newTestMemory(t)

	if err := c.Set(ctx, "short", []byte("1"), 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "default", []byte("2"), 0); err != nil {
		t.Fatal(err)
	}
	c.cache.Wait()

	got, err := c.Get(ctx, "short")
	if err != nil || string(got) != "1" {
		t.Fatalf("Get(short) = %q, %v", got, err)
	}

	time.Sleep(40 * time.Millisecond)

	if _, getErr := c.Get(ctx, "short"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("Get(short) after ttl err = %v, want ErrNotFound", getErr)
	}
	got, err = c.Get(ctx, "default")
	if err != nil || string(got) != "2" {
		t.Fatalf("Get(default) = %q, %v", got, err)
	}
}

func TestMemoryCacheMissingKey(t *testing.T) {
	c := newTestMemory(t)
	ctx := context.Background()

	if _, err := c.Get(ctx, "absent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(absent) err = %v, want ErrNotFound", err)
	}
}
