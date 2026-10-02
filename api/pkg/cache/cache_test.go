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

func TestMemoryCacheReadsItsOwnWrites(t *testing.T) {
	ctx := context.Background()
	c := newTestMemory(t)

	for i := range 1000 {
		key := "state:" + string(rune('a'+i%26)) + time.Duration(i).String()
		if err := c.Set(ctx, key, []byte("1"), time.Minute); err != nil {
			t.Fatalf("Set(%s) err = %v", key, err)
		}
		if _, err := c.Get(ctx, key); err != nil {
			t.Fatalf("Get(%s) right after Set err = %v", key, err)
		}
	}
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

func TestMemoryCacheDelete(t *testing.T) {
	ctx := context.Background()
	c := newTestMemory(t)

	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete err = %v", err)
	}
}

func TestMemoryCacheRejectsOversizedEntry(t *testing.T) {
	ctx := context.Background()
	c := newTestMemory(t)

	big := make([]byte, memoryMaxCost+1)
	if err := c.Set(ctx, "big", big, 0); !errors.Is(err, ErrNotStored) {
		t.Fatalf("Set(big) err = %v, want ErrNotStored", err)
	}
}
