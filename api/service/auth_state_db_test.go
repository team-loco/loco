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
		err := store.Start(t.Context(), "code", []byte("1"), time.Minute)
		if err != nil && !errors.Is(err, errAuthStateExists) {
			t.Error(err)
		}
		return err == nil
	})
	if wins != 1 {
		t.Fatalf("Start stored %d times, want 1", wins)
	}

	expire(t, pool, "code")
	if err := store.Start(t.Context(), "code", []byte("2"), time.Minute); err != nil {
		t.Fatalf("Start over an expired entry: %v", err)
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

func TestAuthStateStartEvictsTheOldestSignIns(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	store.maxEntries = 3
	for _, key := range []string{"a", "b", "c"} {
		if err := store.Start(t.Context(), key, []byte(key), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Set(t.Context(), "device", []byte("d"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Start(t.Context(), "d", []byte("d"), time.Minute); err != nil {
		t.Fatalf("Start on a full store: %v", err)
	}
	if keys := storedKeys(t, pool, "TRUE"); !slices.Equal(keys, []string{"b", "c", "d", "device"}) {
		t.Fatalf("rows after a start on a full store = %v, want the oldest sign-in evicted", keys)
	}

	if _, err := store.Take(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	if err := store.Restore(t.Context(), "b", []byte("b"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Start(t.Context(), "e", []byte("e"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if keys := storedKeys(t, pool, "started_at IS NOT NULL"); !slices.Equal(keys, []string{"b", "d", "e"}) {
		t.Fatalf("started sign-ins = %v, want the restored one kept as the newest", keys)
	}
}

func TestAuthStateStartsConcurrentlyAtTheCap(t *testing.T) {
	t.Parallel()
	pool, store := newAuthStateStore(t)
	store.maxEntries = 4
	var next atomic.Int64
	start := func() error {
		key := "start-" + strconv.FormatInt(next.Add(1), 10)
		return store.Start(t.Context(), key, []byte(key), time.Minute)
	}
	for range store.maxEntries {
		if err := start(); err != nil {
			t.Fatal(err)
		}
	}
	wins := countConcurrent(32, func() bool {
		err := start()
		if err != nil {
			t.Error(err)
		}
		return err == nil
	})
	if wins != 32 {
		t.Fatalf("%d of 32 concurrent starts on a full store succeeded", wins)
	}
	if err := start(); err != nil {
		t.Fatal(err)
	}
	if keys := storedKeys(t, pool, "started_at IS NOT NULL"); len(keys) != int(store.maxEntries) {
		t.Fatalf("%d started sign-ins after the starts settled, want %d", len(keys), store.maxEntries)
	}
}
