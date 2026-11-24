package service

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestAuthStateStartStoresOnce(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	wins := countConcurrent(32, func() bool {
		err := store.Start(t.Context(), time.Minute, startEntry{key: "user-code", value: []byte("1")})
		if err != nil && !errors.Is(err, errAuthStateExists) {
			t.Error(err)
		}
		return err == nil
	})
	if wins != 1 {
		t.Fatalf("Start stored %d times, want 1", wins)
	}

	expire(t, pool, "user-code")
	if err := store.Start(t.Context(), time.Minute, startEntry{key: "user-code", value: []byte("2")}); err != nil {
		t.Fatalf("Start over an expired entry: %v", err)
	}
	if got, getErr := store.Get(t.Context(), "user-code"); getErr != nil || string(got) != "2" {
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
	if keys := storedKeys(t, pool, "TRUE"); !slices.Equal(keys, []string{"fresh"}) {
		t.Fatalf("rows after cleanup = %v, want [fresh]", keys)
	}
}

// storedKeys lists the keys of the rows matching where, in key order.
func storedKeys(t *testing.T, pool *pgxpool.Pool, where string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT key FROM auth_states WHERE "+where+" ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func entry(key string) startEntry {
	return startEntry{key: key, value: []byte(key)}
}

func TestAuthStateStartEvictsTheOldestSignIns(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	store.maxEntries = 3
	start := func(entries ...startEntry) {
		t.Helper()
		if err := store.Start(t.Context(), time.Minute, entries...); err != nil {
			t.Fatal(err)
		}
	}
	start(entry("a-code"), entry("a-device"))
	start(entry("b"))
	start(entry("c"))
	if err := store.Set(t.Context(), "approved", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	start(entry("d"))
	if keys := storedKeys(t, pool, "TRUE"); !slices.Equal(keys, []string{"approved", "b", "c", "d"}) {
		t.Fatalf("rows after a start on a full store = %v, want the oldest sign-in evicted with both its entries", keys)
	}

	taken, err := store.Take(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(t.Context(), "b", taken); err != nil {
		t.Fatal(err)
	}
	start(entry("e"))
	if keys := storedKeys(t, pool, "started_at IS NOT NULL"); !slices.Equal(keys, []string{"c", "d", "e"}) {
		t.Fatalf("started sign-ins = %v, want the restored one evicted as the oldest", keys)
	}
}

func TestAuthStateStartsConcurrentlyAtTheCap(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	store.maxEntries = 20
	const workers, startsEach = 16, 10
	var next atomic.Int64
	start := func() error {
		key := "start-" + strconv.FormatInt(next.Add(1), 10)
		return store.Start(t.Context(), time.Minute, entry(key))
	}
	for range store.maxEntries {
		if err := start(); err != nil {
			t.Fatal(err)
		}
	}
	wins := countConcurrent(workers, func() bool {
		for range startsEach {
			if err := start(); err != nil {
				t.Error(err)
				return false
			}
		}
		return true
	})
	if wins != workers {
		t.Fatalf("%d of %d workers started all their sign-ins on a full store", wins, workers)
	}
	// Each concurrent start can miss the rows the others have not committed yet, so up to workers extra
	// sign-ins remain.
	if started := len(storedKeys(t, pool, "started_at IS NOT NULL")); started > int(store.maxEntries)+workers {
		t.Fatalf("%d started sign-ins after %d concurrent starts, want at most %d",
			started, workers*startsEach, int(store.maxEntries)+workers)
	}
}
