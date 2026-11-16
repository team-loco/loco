package dbtest_test

import (
	"context"
	"testing"

	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestEventsReadsCommittedRowsWithAnotherDatabaseTransactionOpen(t *testing.T) {
	other := dbtest.NewPool(t)
	pool := dbtest.NewPool(t)
	tx, err := other.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			t.Error(rollbackErr)
		}
	}()
	if _, err = tx.Exec(t.Context(), "SELECT pg_current_xact_id()"); err != nil {
		t.Fatal(err)
	}
	q := genDb.New(pool)
	const count = 101
	for range count {
		if err = events.Record(
			t.Context(), q, events.Event{Type: events.ClusterRegistered, ActorType: events.ActorSystem},
		); err != nil {
			t.Fatal(err)
		}
	}
	recorded := dbtest.Events(t, pool)
	if len(recorded) != count {
		t.Fatalf("recorded events = %d, want %d", len(recorded), count)
	}
	for i, event := range recorded {
		if event.Type != events.ClusterRegistered || (i > 0 && event.Seq <= recorded[i-1].Seq) {
			t.Fatalf("event %d = %+v", i, event)
		}
	}
}
