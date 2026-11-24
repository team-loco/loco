package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// countConcurrent runs op on n goroutines at once and counts the calls that report true.
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

func newAuthStateStore(t *testing.T) (*pgxpool.Pool, pgAuthStore) {
	t.Helper()
	pool := dbtest.NewPool(t)
	return pool, pgAuthStore{queries: genDb.New(pool), maxEntries: maxAuthStates}
}

func expire(t *testing.T, pool *pgxpool.Pool, key string) {
	t.Helper()
	const query = "UPDATE auth_states SET expires_at = NOW() - INTERVAL '1 second' WHERE key = $1"
	if _, err := pool.Exec(t.Context(), query, key); err != nil {
		t.Fatal(err)
	}
}

func TestAuthStateTakeSucceedsOnce(t *testing.T) {
	t.Parallel()
	_, store := newAuthStateStore(t)
	if err := store.Set(t.Context(), "take", []byte("1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	wins := countConcurrent(32, func() bool {
		_, err := store.Take(t.Context(), "take")
		if err != nil && !errors.Is(err, errAuthStateNotFound) {
			t.Error(err)
		}
		return err == nil
	})
	if wins != 1 {
		t.Fatalf("Take succeeded %d times, want 1", wins)
	}
	if _, err := store.Get(t.Context(), "take"); !errors.Is(err, errAuthStateNotFound) {
		t.Fatalf("Get after Take err = %v, want errAuthStateNotFound", err)
	}
}

func TestAuthStateSetIfAbsentStoresOnce(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	wins := countConcurrent(32, func() bool {
		stored, err := store.SetIfAbsent(t.Context(), "code", []byte("1"), time.Minute)
		if err != nil {
			t.Error(err)
		}
		return stored
	})
	if wins != 1 {
		t.Fatalf("SetIfAbsent stored %d times, want 1", wins)
	}

	expire(t, pool, "code")
	stored, err := store.SetIfAbsent(t.Context(), "code", []byte("2"), time.Minute)
	if err != nil || !stored {
		t.Fatalf("SetIfAbsent over an expired entry = %v, %v", stored, err)
	}
	if got, getErr := store.Get(t.Context(), "code"); getErr != nil || string(got) != "2" {
		t.Fatalf("Get = %q, %v", got, getErr)
	}
}

func TestAuthStateExpires(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	for _, key := range []string{"fresh", "stale"} {
		if err := store.Set(t.Context(), key, []byte(key), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	expire(t, pool, "stale")

	if _, err := store.Get(t.Context(), "stale"); !errors.Is(err, errAuthStateNotFound) {
		t.Fatalf("Get(stale) err = %v, want errAuthStateNotFound", err)
	}
	if _, err := store.Take(t.Context(), "stale"); !errors.Is(err, errAuthStateNotFound) {
		t.Fatalf("Take(stale) err = %v, want errAuthStateNotFound", err)
	}
	if err := genDb.New(pool).DeleteExpiredAuthStates(t.Context()); err != nil {
		t.Fatal(err)
	}
	var keys []string
	rows, err := pool.Query(t.Context(), "SELECT key FROM auth_states")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if scanErr := rows.Scan(&key); scanErr != nil {
			t.Fatal(scanErr)
		}
		keys = append(keys, key)
	}
	if len(keys) != 1 || keys[0] != "fresh" {
		t.Fatalf("rows after cleanup = %v, want [fresh]", keys)
	}
}

func TestAuthStateHoldsAtMostMaxEntries(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	store.maxEntries = 2
	for _, key := range []string{"one", "two"} {
		if err := store.Set(t.Context(), key, []byte(key), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Set(t.Context(), "third", []byte("3"), time.Minute); !errors.Is(err, errAuthStateFull) {
		t.Fatalf("Set(third) on a full store err = %v, want errAuthStateFull", err)
	}
	if stored, err := store.SetIfAbsent(t.Context(), "third", []byte("3"), time.Minute); err != nil || stored {
		t.Fatalf("SetIfAbsent(third) on a full store = %v, %v", stored, err)
	}
	if err := store.Set(t.Context(), "one", []byte("updated"), time.Minute); err != nil {
		t.Fatalf("Set(one) updating a stored key on a full store: %v", err)
	}

	expire(t, pool, "two")
	if err := store.Set(t.Context(), "third", []byte("3"), time.Minute); err != nil {
		t.Fatalf("Set(third) after an entry expired: %v", err)
	}
}
