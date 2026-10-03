package cache

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testCaches(t *testing.T) map[string]Cache {
	t.Helper()
	caches := map[string]Cache{"memory": newTestMemory(t)}
	addr := os.Getenv("LOCO_TEST_VALKEY_URL")
	if addr == "" {
		return caches
	}
	v, err := NewValkey(addr, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeErr := v.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})
	caches["valkey"] = v
	return caches
}

func countConcurrent(n int, op func() bool) int64 {
	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Go(func() {
			<-start
			if op() {
				wins.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	return wins.Load()
}

func TestSetIfAbsentStoresOnce(t *testing.T) {
	for name, c := range testCaches(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := "test:setifabsent:" + name + ":" + time.Now().String()

			wins := countConcurrent(32, func() bool {
				stored, err := c.SetIfAbsent(ctx, key, []byte("1"), time.Minute)
				if err != nil {
					t.Error(err)
				}
				return stored
			})
			if wins != 1 {
				t.Fatalf("SetIfAbsent stored %d times, want 1", wins)
			}
			if _, err := c.Get(ctx, key); err != nil {
				t.Fatalf("Get after SetIfAbsent err = %v", err)
			}
		})
	}
}

func TestTakeSucceedsOnce(t *testing.T) {
	for name, c := range testCaches(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := "test:take:" + name + ":" + time.Now().String()
			if err := c.Set(ctx, key, []byte("1"), time.Minute); err != nil {
				t.Fatal(err)
			}

			wins := countConcurrent(32, func() bool {
				existed, err := c.Take(ctx, key)
				if err != nil {
					t.Error(err)
				}
				return existed
			})
			if wins != 1 {
				t.Fatalf("Take succeeded %d times, want 1", wins)
			}
			existed, err := c.Take(ctx, "test:take:absent:"+name)
			if err != nil || existed {
				t.Fatalf("Take(absent) = %v, %v", existed, err)
			}
		})
	}
}
