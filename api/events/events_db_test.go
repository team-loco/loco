package events_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/reqctx"
)

func TestRecordFillsActorScopeAndRequest(t *testing.T) {
	pool := dbtest.NewPool(t)
	q := genDb.New(pool)
	ctx := t.Context()

	owner, err := q.CreateUser(ctx, genDb.CreateUserParams{Email: "owner@acme.test"})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	org, err := q.CreateOrganization(ctx, genDb.CreateOrganizationParams{Name: "acme", CreatedBy: owner.ID})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	var wsID uuid.UUID
	if scanErr := pool.QueryRow(ctx,
		"INSERT INTO workspaces (org_id, name, created_by) VALUES ($1, 'default', $2) RETURNING id",
		org.ID, owner.ID).Scan(&wsID); scanErr != nil {
		t.Fatalf("workspace: %v", scanErr)
	}

	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: owner.ID}
	reqCtx := reqctx.WithPrincipal(ctx, reqctx.Principal{
		Entity:    entity,
		ActorType: genDb.DeploymentActorTypeUser,
		ActorID:   owner.ID,
	})
	reqCtx = reqctx.WithRequest(reqCtx, reqctx.Request{ID: "req-1"})
	if recordErr := events.Record(reqCtx, q, events.Event{
		Type:        events.EnvironmentCreated,
		WorkspaceID: new(wsID),
		SubjectType: "environment",
		SubjectID:   new(uuid.New()),
		Data:        map[string]any{"name": "staging"},
	}); recordErr != nil {
		t.Fatalf("record: %v", recordErr)
	}
	if recordErr := events.Record(
		reqCtx,
		q,
		events.Event{Type: events.ClusterRegistered, ActorType: "agent"},
	); recordErr != nil {
		t.Fatalf("record agent event: %v", recordErr)
	}

	rows, err := events.ListAudit(ctx, pool, events.AuditQuery{OrgID: org.ID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("org events = %d (%v)", len(rows), err)
	}
	if rows[0].ActorEmail == nil || *rows[0].ActorEmail != "owner@acme.test" {
		t.Fatalf("actor email = %v", rows[0].ActorEmail)
	}
	ev := rows[0].Event
	if ev.ActorType != "user" || ev.ActorID == nil || *ev.ActorID != owner.ID {
		t.Fatalf("actor = %s %v", ev.ActorType, ev.ActorID)
	}
	if ev.RequestID == nil || *ev.RequestID != "req-1" || ev.WorkspaceID == nil || *ev.WorkspaceID != wsID {
		t.Fatalf("event = %+v", ev)
	}
	var data map[string]any
	if jsonErr := json.Unmarshal(ev.Data, &data); jsonErr != nil || data["name"] != "staging" {
		t.Fatalf("data = %s (%v)", ev.Data, jsonErr)
	}

	all := dbtest.Events(t, pool)
	if len(all) != 2 || all[1].ActorType != "agent" || all[0].Seq >= all[1].Seq {
		t.Fatalf("recorded events = %+v", all)
	}

	if _, updateErr := pool.Exec(ctx, "UPDATE events SET type = 'tampered'"); updateErr == nil {
		t.Fatal("events could be updated")
	}

	deleted, err := q.DeleteEventsBefore(
		ctx,
		genDb.DeleteEventsBeforeParams{Cutoff: time.Now().Add(time.Minute), BatchSize: 10},
	)
	if err != nil || deleted != 0 {
		t.Fatalf("unpublished deleted %d (%v)", deleted, err)
	}
	if publishErr := events.Publish(ctx, pool); publishErr != nil {
		t.Fatal(publishErr)
	}
	deleted, err = q.DeleteEventsBefore(ctx, genDb.DeleteEventsBeforeParams{
		Cutoff:    time.Now().Add(time.Minute),
		BatchSize: 1,
	})
	if err != nil || deleted != 1 {
		t.Fatalf("one batch deleted %d (%v)", deleted, err)
	}
	deleted, err = q.DeleteEventsBefore(ctx, genDb.DeleteEventsBeforeParams{
		Cutoff:    time.Now().Add(time.Minute),
		BatchSize: 10,
	})
	if err != nil || deleted != 1 {
		t.Fatalf("retention deleted %d (%v)", deleted, err)
	}
}

func TestRecordStoresTheCallersTraceparent(t *testing.T) {
	tracingtest.Record(t)
	pool := dbtest.NewPool(t)
	q := genDb.New(pool)
	traced := tracingtest.StartParent(t)
	if err := events.Record(
		traced,
		q,
		events.Event{Type: events.ClusterRegistered, ActorType: events.ActorSystem},
	); err != nil {
		t.Fatalf("record traced event: %v", err)
	}
	if err := events.Record(
		t.Context(),
		q,
		events.Event{Type: events.OrgUpdated, ActorType: events.ActorSystem},
	); err != nil {
		t.Fatalf("record untraced event: %v", err)
	}
	stored := map[string]*string{}
	rows, err := pool.Query(t.Context(), "SELECT type, traceparent FROM events")
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var eventType string
		var traceparent *string
		if scanErr := rows.Scan(&eventType, &traceparent); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		stored[eventType] = traceparent
	}
	want := tracing.Traceparent(traced)
	if got := stored[events.ClusterRegistered]; got == nil || *got != want {
		t.Fatalf("traced event traceparent = %v, want %q", got, want)
	}
	if got := stored[events.OrgUpdated]; got != nil {
		t.Fatalf("untraced event traceparent = %q, want NULL", *got)
	}
}

func TestRetentionRunRecordsARootSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	pool := dbtest.NewPool(t)
	q := genDb.New(pool)
	request := tracingtest.StartParent(t)
	events.DeleteExpired(request, q, time.Hour)
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "events.retention" {
		t.Fatalf("ended spans = %d, want the retention span", len(ended))
	}
	if ended[0].Parent().IsValid() {
		t.Fatalf("retention span has parent %v", ended[0].Parent())
	}
}

func TestDeleteExpiredRemovesEveryBatchAndSparesRecentEvents(t *testing.T) {
	pool := dbtest.NewPool(t)
	q := genDb.New(pool)
	expired := 2*events.RetentionBatchSize + 1
	_, err := pool.Exec(t.Context(), `
		INSERT INTO events (type, actor_type, created_at)
		SELECT 'org.created', 'system', now() - interval '2 hours' FROM generate_series(1, $1)`, expired)
	if err != nil {
		t.Fatalf("insert expired events: %v", err)
	}
	_, err = pool.Exec(t.Context(), "INSERT INTO events (type, actor_type) VALUES ('org.created', 'system')")
	if err != nil {
		t.Fatalf("insert recent event: %v", err)
	}
	for range expired/500 + 1 {
		if publishErr := events.Publish(t.Context(), pool); publishErr != nil {
			t.Fatal(publishErr)
		}
	}
	events.DeleteExpired(t.Context(), q, time.Hour)
	var left int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM events").Scan(&left); err != nil || left != 1 {
		t.Fatalf("events left = %d (%v), want the recent one", left, err)
	}
}
