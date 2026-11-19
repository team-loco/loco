package migrations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/team-loco/loco/internal/clickhouse/migrationlock"
)

const (
	racingRunners = 8
	// A holder gets a lease longer than renewals take on a loaded machine.
	testLockTTL       = 2 * time.Minute
	testRetryPeriod   = 200 * time.Millisecond
	appliedPollPeriod = 20 * time.Millisecond
	createLedger      = "-- +goose NO TRANSACTION\n-- +goose Up\n" +
		"CREATE TABLE IF NOT EXISTS ledger (entry UInt8) ENGINE = MergeTree ORDER BY entry;\n"
	recordEntry = "-- +goose NO TRANSACTION\n-- +goose Up\nINSERT INTO ledger VALUES (1);\n"
)

var (
	// The holder's slow step outlasts any wait for migration 1, so the holder is always killed during it.
	abandonedStep           = "-- +goose NO TRANSACTION\n-- +goose Up\n" + strings.Repeat("SELECT sleep(3);\n", 200)
	resumedStep             = "-- +goose NO TRANSACTION\n-- +goose Up\nSELECT 1;\n"
	nonIdempotentMigrations = fstest.MapFS{
		"00001_create_ledger.sql": {Data: []byte(createLedger)},
		"00002_record_entry.sql":  {Data: []byte(recordEntry)},
	}
	abandonedMigrations = interruptibleMigrations(abandonedStep)
	resumedMigrations   = interruptibleMigrations(resumedStep)
)

func interruptibleMigrations(slowStep string) fstest.MapFS {
	return fstest.MapFS{
		"00001_create_ledger.sql": {Data: []byte(createLedger)},
		"00002_slow_step.sql":     {Data: []byte(slowStep)},
		"00003_record_entry.sql":  {Data: []byte(recordEntry)},
	}
}

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
	for {
		var count uint64
		if err := d.admin.QueryRow(t.Context(), query, version).Scan(&count); err == nil && count > 0 {
			return
		}
		time.Sleep(appliedPollPeriod)
	}
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
			_, err := apply(ctx, db.cfg, abandonedMigrations)
			return err
		})
	}()
	db.waitForVersion(t, 1)
	killHolder()
	if err := <-holderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("killed holder WithLock() = %v, want %v", err, context.Canceled)
	}

	err := newLock(db).WithLock(t.Context(), func(ctx context.Context) error {
		_, err := apply(ctx, db.cfg, resumedMigrations)
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
	if len(versions) != len(resumedMigrations)+1 {
		t.Fatalf("applied versions = %v, want every migration", versions)
	}
}
