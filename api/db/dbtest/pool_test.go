package dbtest

import (
	"context"
	"fmt"
	"os"
	"testing"

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
	if err := dead.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := reapDeadRuns(t.Context(), connect()); err != nil {
		t.Fatal(err)
	}
	for run, want := range map[int64]bool{liveRun: true, deadRun: false} {
		var exists bool
		if err := live.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
			name(run)).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != want {
			t.Errorf("database of run %d exists = %v, want %v", run, exists, want)
		}
	}
}
