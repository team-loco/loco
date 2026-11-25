package dbtest

import (
	"testing"

	"github.com/google/uuid"
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

// InstallEvents returns the data of the installation-level events of the type about the subject, oldest first.
func InstallEvents(t *testing.T, pool *pgxpool.Pool, eventType string, subject uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := pool.Query(
		t.Context(),
		`SELECT data FROM events WHERE type = $1 AND subject_id = $2 AND org_id IS NULL ORDER BY seq`,
		eventType,
		subject,
	)
	if err != nil {
		t.Fatalf("query install events: %v", err)
	}
	data, err := pgx.CollectRows(rows, pgx.RowTo[map[string]any])
	if err != nil {
		t.Fatalf("read install events: %v", err)
	}
	return data
}

// InstallEventTypes returns the types of the installation-level events about the subject, oldest first.
func InstallEventTypes(t *testing.T, pool *pgxpool.Pool, subject uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT type FROM events WHERE subject_id = $1 AND org_id IS NULL ORDER BY seq`, subject)
	if err != nil {
		t.Fatalf("query install event types: %v", err)
	}
	types, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read install event types: %v", err)
	}
	return types
}
