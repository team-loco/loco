package migrationlock_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	testClickHouseEnv = "LOCO_TEST_CLICKHOUSE_URL"
	databaseNameBytes = 4
	contenders        = 4
	holdTime          = 100 * time.Millisecond
	testTTL           = 2 * time.Second
	testRetryPeriod   = 200 * time.Millisecond
	waitTimeout       = 500 * time.Millisecond
	settleDeadline    = 30 * time.Second
	deadHolder        = "dead-holder"
	otherHolder       = "other-holder"
)

var errMigration = errors.New("migration failed")

func TestValidateRejectsInvalidConfig(t *testing.T) {
	cases := map[string]struct {
		cfg  migrationlock.Config
		want error
	}{
		"zero ttl": {
			cfg:  migrationlock.Config{TTL: 0, RetryPeriod: time.Second},
			want: migrationlock.ErrNotPositive,
		},
		"negative retry": {
			cfg:  migrationlock.Config{TTL: time.Minute, RetryPeriod: -time.Second},
			want: migrationlock.ErrNotPositive,
		},
		"retry above half the ttl": {
			cfg:  migrationlock.Config{TTL: 3 * time.Second, RetryPeriod: 2 * time.Second},
			want: migrationlock.ErrRetryPeriod,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := tc.cfg.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
	valid := migrationlock.Config{TTL: testTTL, RetryPeriod: testRetryPeriod}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on a valid config = %v", err)
	}
}

type harness struct {
	admin driver.Conn
	cfg   migrationlock.Config
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv(testClickHouseEnv)
	if dsn == "" {
		t.Skip(testClickHouseEnv + " not set")
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", testClickHouseEnv, err)
	}
	admin, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	suffix := make([]byte, databaseNameBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	database := "loco_lock_test_" + hex.EncodeToString(suffix)
	t.Cleanup(func() {
		if err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+database+" SYNC"); err != nil {
			t.Errorf("drop %s: %v", database, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close clickhouse: %v", err)
		}
	})
	return &harness{
		admin: admin,
		cfg:   migrationlock.Config{DSN: dsn, Database: database, TTL: testTTL, RetryPeriod: testRetryPeriod},
	}
}

func (h *harness) table() string {
	return h.cfg.Database + "." + migrationlock.Table
}

func (h *harness) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx := clickhouse.Context(t.Context(), clickhouse.WithSettings(clickhouse.Settings{"keeper_map_strict_mode": 1}))
	if err := h.admin.Exec(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func (h *harness) rows(t *testing.T) uint64 {
	t.Helper()
	var count uint64
	if err := h.admin.QueryRow(t.Context(), "SELECT count() FROM "+h.table()).Scan(&count); err != nil {
		t.Fatalf("count lock rows: %v", err)
	}
	return count
}

func (h *harness) holdUntil(t *testing.T, started chan<- struct{}, release <-chan struct{}) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- migrationlock.New(h.cfg).WithLock(t.Context(), func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()
	return done
}

func TestWithLockRunsOneHolderAtATime(t *testing.T) {
	h := newHarness(t)
	var holding, most atomic.Int32
	var wg sync.WaitGroup
	errs := make([]error, contenders)
	for i := range contenders {
		wg.Go(func() {
			errs[i] = migrationlock.New(h.cfg).WithLock(t.Context(), func(context.Context) error {
				now := holding.Add(1)
				for {
					seen := most.Load()
					if now <= seen || most.CompareAndSwap(seen, now) {
						break
					}
				}
				time.Sleep(holdTime)
				holding.Add(-1)
				return nil
			})
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("contender %d: %v", i, err)
		}
	}
	if got := most.Load(); got != 1 {
		t.Fatalf("at most %d holders ran at once, want 1", got)
	}
	if rows := h.rows(t); rows != 0 {
		t.Fatalf("lock rows after every holder finished = %d, want 0", rows)
	}
}

func TestWithLockReturnsTheMigrationErrorAndReleases(t *testing.T) {
	h := newHarness(t)
	err := migrationlock.New(h.cfg).WithLock(t.Context(), func(context.Context) error {
		return errMigration
	})
	if !errors.Is(err, errMigration) {
		t.Fatalf("WithLock() = %v, want %v", err, errMigration)
	}
	if rows := h.rows(t); rows != 0 {
		t.Fatalf("lock rows after a failed migration = %d, want 0", rows)
	}
}

func TestWithLockGivesUpWhenContextEndsWhileWaiting(t *testing.T) {
	h := newHarness(t)
	started := make(chan struct{})
	release := make(chan struct{})
	holderDone := h.holdUntil(t, started, release)
	<-started

	ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
	defer cancel()
	ran := false
	err := migrationlock.New(h.cfg).WithLock(ctx, func(context.Context) error {
		ran = true
		return nil
	})
	if !errors.Is(err, migrationlock.ErrNotAcquired) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"WithLock() while held = %v, want %v and %v",
			err,
			migrationlock.ErrNotAcquired,
			context.DeadlineExceeded,
		)
	}
	if ran {
		t.Fatal("waiter ran its migration without the lock")
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithLock(): %v", err)
	}
}

func TestDeadHolderLockIsTakenAfterItExpires(t *testing.T) {
	h := newHarness(t)
	err := migrationlock.New(h.cfg).WithLock(t.Context(), func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("create lock table: %v", err)
	}
	h.exec(t, "INSERT INTO "+h.table()+" SELECT 'schema', ?, now64(3) + toIntervalMillisecond(?)",
		deadHolder, testTTL.Milliseconds())

	diedAt := time.Now()
	var acquiredAt time.Time
	err = migrationlock.New(h.cfg).WithLock(t.Context(), func(context.Context) error {
		acquiredAt = time.Now()
		return nil
	})
	if err != nil {
		t.Fatalf("successor WithLock(): %v", err)
	}
	waited := acquiredAt.Sub(diedAt)
	t.Logf("successor took the lock %s after the dead holder's last renewal (ttl %s)", waited, testTTL)
	if waited < testTTL-testRetryPeriod {
		t.Fatalf("successor took the lock after %s, before the %s ttl expired", waited, testTTL)
	}
	if waited > settleDeadline {
		t.Fatalf("successor took %s to take an expired lock", waited)
	}
}

func TestHolderStopsWhenTheLockIsTakenOver(t *testing.T) {
	h := newHarness(t)
	err := migrationlock.New(h.cfg).WithLock(t.Context(), func(ctx context.Context) error {
		h.exec(t, "ALTER TABLE "+h.table()+" UPDATE holder = ? WHERE key = 'schema'", otherHolder)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(settleDeadline):
			return nil
		}
	})
	if !errors.Is(err, migrationlock.ErrLockLost) {
		t.Fatalf("WithLock() after a takeover = %v, want %v", err, migrationlock.ErrLockLost)
	}
	if rows := h.rows(t); rows != 1 {
		t.Fatalf("lock rows after a takeover = %d, want the new holder's row", rows)
	}
}

func TestHolderThatCannotRenewStopsWithinTheTTL(t *testing.T) {
	h := newHarness(t)
	var stoppedAfter time.Duration
	err := migrationlock.New(h.cfg).WithLock(t.Context(), func(ctx context.Context) error {
		h.exec(t, "DROP TABLE "+h.table()+" SYNC")
		cutAt := time.Now()
		select {
		case <-ctx.Done():
			stoppedAfter = time.Since(cutAt)
			return ctx.Err()
		case <-time.After(settleDeadline):
			return nil
		}
	})
	if !errors.Is(err, migrationlock.ErrLockLost) {
		t.Fatalf("WithLock() without renewals = %v, want %v", err, migrationlock.ErrLockLost)
	}
	t.Logf("holder stopped %s after renewals started failing (ttl %s)", stoppedAfter, testTTL)
	if stoppedAfter > testTTL+testRetryPeriod {
		t.Fatalf("holder kept migrating %s after renewals failed, past the %s ttl", stoppedAfter, testTTL)
	}
}
