package migrations

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	racingRunners      = 8
	testLockTTL        = 2 * time.Second
	testRetryPeriod    = 200 * time.Millisecond
	appliedPollPeriod  = 20 * time.Millisecond
	appliedPollTimeout = 30 * time.Second
	createLedger       = "-- +goose NO TRANSACTION\n-- +goose Up\n" +
		"CREATE TABLE IF NOT EXISTS ledger (entry UInt8) ENGINE = MergeTree ORDER BY entry;\n"
	recordEntry = "-- +goose NO TRANSACTION\n-- +goose Up\nINSERT INTO ledger VALUES (1);\n"
	slowStep    = "-- +goose NO TRANSACTION\n-- +goose Up\nSELECT sleep(2);\n"
)

var (
	nonIdempotentMigrations = fstest.MapFS{
		"00001_create_ledger.sql": {Data: []byte(createLedger)},
		"00002_record_entry.sql":  {Data: []byte(recordEntry)},
	}
	interruptibleMigrations = fstest.MapFS{
		"00001_create_ledger.sql": {Data: []byte(createLedger)},
		"00002_slow_step.sql":     {Data: []byte(slowStep)},
		"00003_record_entry.sql":  {Data: []byte(recordEntry)},
	}
)

func (d *testDatabase) ledgerRows(t *testing.T) uint64 {
	t.Helper()
	var count uint64
	if err := d.admin.QueryRow(t.Context(), "SELECT count() FROM "+d.cfg.Database+".ledger").Scan(&count); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return count
}

func (d *testDatabase) waitForVersion(t *testing.T, version int64) {
	t.Helper()
	query := "SELECT count() FROM " + d.cfg.Database + "." + VersionTable + " WHERE version_id = ?"
	deadline := time.Now().Add(appliedPollTimeout)
	for time.Now().Before(deadline) {
		var count uint64
		if err := d.admin.QueryRow(t.Context(), query, version).Scan(&count); err == nil && count > 0 {
			return
		}
		time.Sleep(appliedPollPeriod)
	}
	t.Fatalf("migration %d was not applied within %s", version, appliedPollTimeout)
}

func newLock(d *testDatabase) *migrationlock.Lock {
	return migrationlock.New(migrationlock.Config{
		DSN:         d.cfg.DSN,
		Database:    d.cfg.Database,
		TTL:         testLockTTL,
		RetryPeriod: testRetryPeriod,
	})
}

func race(t *testing.T, run func(runner int) error) []error {
	t.Helper()
	errs := make([]error, racingRunners)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for runner := range racingRunners {
		wg.Go(func() {
			<-start
			errs[runner] = run(runner)
		})
	}
	close(start)
	wg.Wait()
	return errs
}

func TestUnlockedRunnersApplyANonIdempotentMigrationMoreThanOnce(t *testing.T) {
	db := newTestDatabase(t)
	errs := race(t, func(int) error {
		_, err := apply(t.Context(), db.cfg, nonIdempotentMigrations)
		return err
	})
	for runner, err := range errs {
		t.Logf("unlocked runner %d: %v", runner, err)
	}
	rows := db.ledgerRows(t)
	t.Logf("unlocked: %d runners inserted %d ledger rows", racingRunners, rows)
	if rows <= 1 {
		t.Fatalf("unlocked runners inserted %d ledger rows, want more than 1 to show the race", rows)
	}
}

func TestLockedRunnersApplyANonIdempotentMigrationOnce(t *testing.T) {
	db := newTestDatabase(t)
	errs := race(t, func(int) error {
		return newLock(db).WithLock(t.Context(), func(ctx context.Context) error {
			_, err := apply(ctx, db.cfg, nonIdempotentMigrations)
			return err
		})
	})
	for runner, err := range errs {
		if err != nil {
			t.Errorf("locked runner %d: %v", runner, err)
		}
	}
	rows := db.ledgerRows(t)
	t.Logf("locked: %d runners inserted %d ledger rows", racingRunners, rows)
	if rows != 1 {
		t.Fatalf("locked runners inserted %d ledger rows, want 1", rows)
	}
}

func TestSuccessorFinishesAMigrationItsHolderAbandoned(t *testing.T) {
	db := newTestDatabase(t)
	holderCtx, killHolder := context.WithCancel(t.Context())
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- newLock(db).WithLock(holderCtx, func(ctx context.Context) error {
			_, err := apply(ctx, db.cfg, interruptibleMigrations)
			return err
		})
	}()
	db.waitForVersion(t, 1)
	killHolder()
	if err := <-holderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("killed holder WithLock() = %v, want %v", err, context.Canceled)
	}

	err := newLock(db).WithLock(t.Context(), func(ctx context.Context) error {
		_, err := apply(ctx, db.cfg, interruptibleMigrations)
		return err
	})
	if err != nil {
		t.Fatalf("successor WithLock(): %v", err)
	}
	if rows := db.ledgerRows(t); rows != 1 {
		t.Fatalf("ledger rows = %d, want 1", rows)
	}
	versions := db.appliedVersions(t)
	t.Logf("applied versions: %v", versions)
	if len(versions) != len(interruptibleMigrations)+1 {
		t.Fatalf("applied versions = %v, want every migration", versions)
	}
}
