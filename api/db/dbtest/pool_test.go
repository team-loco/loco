package dbtest

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReapDropsOnlyTheDatabasesOfDeadRuns(t *testing.T) {
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	connect := func() *pgx.Conn {
		conn, err := pgx.Connect(t.Context(), baseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := conn.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		return conn
	}
	const liveRun, deadRun = 7_000_000_001, 7_000_000_002
	name := func(run int64) string { return fmt.Sprintf("%s%d_reaptest", testDatabasePrefix, run) }
	live, dead := connect(), connect()
	for _, owner := range []struct {
		conn *pgx.Conn
		run  int64
	}{{live, liveRun}, {dead, deadRun}} {
		if _, err := owner.conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", owner.run); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.conn.Exec(
			t.Context(),
			"CREATE DATABASE "+pgx.Identifier{name(owner.run)}.Sanitize(),
		); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			adminExec(t, baseURL, "DROP DATABASE IF EXISTS "+pgx.Identifier{name(owner.run)}.Sanitize()+" WITH (FORCE)")
		})
	}
	// The server frees a closed session's locks after the client returns, so release it first.
	if _, err := dead.Exec(t.Context(), "SELECT pg_advisory_unlock($1)", int64(deadRun)); err != nil {
		t.Fatal(err)
	}
	if err := dead.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := reapDeadRuns(t.Context(), connect()); err != nil {
		t.Fatal(err)
	}
	exists := func(run int64) bool {
		var found bool
		if err := live.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
			name(run)).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	// Another package's reaper may hold the dead run's lock while it drops the same database.
	deadline := time.Now().Add(5 * time.Second)
	for exists(deadRun) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if exists(deadRun) {
		t.Errorf("database of run %d still exists", deadRun)
	}
	if !exists(liveRun) {
		t.Errorf("database of run %d was dropped", liveRun)
	}
}

func TestRunStaysLockedAfterGarbageCollection(t *testing.T) {
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	owner, err := run()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.GC()
	other, err := pgx.Connect(t.Context(), baseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := other.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var free bool
	if err := other.QueryRow(t.Context(), "SELECT pg_try_advisory_lock($1)", owner.id).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free {
		t.Fatal("another session took the lock of a live run")
	}
}
