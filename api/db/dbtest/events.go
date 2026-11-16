package dbtest

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
)

// Events returns the fixture's committed events in insertion order.
func Events(t *testing.T, pool *pgxpool.Pool) []genDb.Event {
	t.Helper()
	rows, err := pool.Query(t.Context(), "SELECT * FROM events ORDER BY seq")
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByName[genDb.Event])
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	return events
}
