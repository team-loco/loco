package events_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestPublisherIgnoresOtherDatabasesAndRollbacks(t *testing.T) {
	pool, other := dbtest.NewPool(t), dbtest.NewPool(t)
	tx, err := other.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, tx)
	if _, err = tx.Exec(t.Context(), "SELECT pg_current_xact_id()"); err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	if err = events.Record(
		t.Context(),
		genDb.New(pool),
		events.Event{Type: events.OrgCreated, ActorType: events.ActorSystem, OrgID: &org},
	); err != nil {
		t.Fatal(err)
	}
	rollback, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, rollback)
	if err = events.Record(
		t.Context(),
		genDb.New(rollback),
		events.Event{Type: events.OrgDeleted, ActorType: events.ActorSystem},
	); err != nil {
		t.Fatal(err)
	}
	if err = rollback.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = events.Publish(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	rows, err := genDb.New(pool).ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 10})
	if err != nil || len(rows) != 1 || rows[0].Type != events.OrgCreated {
		t.Fatalf("stream = %v (%v)", rows, err)
	}
	audit, err := events.ListAudit(t.Context(), pool, events.AuditQuery{OrgID: org, Stream: true, Limit: 10})
	if err != nil || len(audit) != 1 {
		t.Fatalf("audit stream = %v (%v)", audit, err)
	}
}

func TestConcurrentPublishersAssignGapFreePositions(t *testing.T) {
	pool := dbtest.NewPool(t)
	if _, err := pool.Exec(
		t.Context(),
		"INSERT INTO events(type, actor_type) SELECT 'org.created', 'system' FROM generate_series(1, 1000)",
	); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 2 {
		wg.Go(func() {
			<-start
			if err := events.Publish(t.Context(), pool); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	rows, err := genDb.New(pool).ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 1001})
	if err != nil || len(rows) != 1000 {
		t.Fatalf("published %d (%v)", len(rows), err)
	}
	for i, row := range rows {
		if row.StreamPosition == nil || *row.StreamPosition != int64(i+1) {
			t.Fatalf("position %d = %v", i, row.StreamPosition)
		}
	}
	if err = events.Publish(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	rows, err = genDb.New(pool).
		ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{AfterPosition: 1000, MaxRows: 1})
	if err != nil || len(rows) != 0 {
		t.Fatalf("duplicate publication: %v (%v)", rows, err)
	}
}

func TestPublisherStreamsLateCommitOnce(t *testing.T) {
	pool := dbtest.NewPool(t)
	org := uuid.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rollbackOnCleanup(t, tx)
	first := events.Event{Type: events.OrgCreated, ActorType: events.ActorSystem, OrgID: &org}
	if err = events.Record(t.Context(), genDb.New(tx), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Type = events.OrgUpdated
	if err = events.Record(t.Context(), genDb.New(pool), second); err != nil {
		t.Fatal(err)
	}
	if err = events.Publish(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	query := events.AuditQuery{OrgID: org, Stream: true, Limit: 10}
	rows, err := events.ListAudit(t.Context(), pool, query)
	if err != nil || len(rows) != 1 || rows[0].Type != second.Type {
		t.Fatalf("first batch = %v (%v)", rows, err)
	}
	query.AfterPosition = *rows[0].StreamPosition
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = events.Publish(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	rows, err = events.ListAudit(t.Context(), pool, query)
	if err != nil || len(rows) != 1 || rows[0].Type != first.Type {
		t.Fatalf("late batch = %v (%v)", rows, err)
	}
	query.AfterPosition = *rows[0].StreamPosition
	rows, err = events.ListAudit(t.Context(), pool, query)
	if err != nil || len(rows) != 0 {
		t.Fatalf("duplicates = %v (%v)", rows, err)
	}
}

func rollbackOnCleanup(t *testing.T, tx pgx.Tx) {
	t.Helper()
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	})
}
