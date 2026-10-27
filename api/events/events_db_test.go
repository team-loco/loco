package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/pkg/tracingtest"
)

func TestRecordFillsActorScopeAndRequest(t *testing.T) {
	pool := authtest.NewPool(t)
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

	reqCtx := context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{Type: genDb.EntityTypeUser, ID: owner.ID})
	reqCtx = context.WithValue(reqCtx, contextkeys.RequestIDKey, "req-1")
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
		ctx,
		q,
		events.Event{Type: events.ClusterRegistered, ActorType: "agent"},
	); recordErr != nil {
		t.Fatalf("record agent event: %v", recordErr)
	}

	rows, err := q.ListOrgAuditEvents(
		ctx,
		genDb.ListOrgAuditEventsParams{OrgID: &org.ID, Types: []string{}, MaxRows: 10},
	)
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

	authtest.WaitForEarlierTransactions(t, pool)
	all, err := q.ListEventsAfter(ctx, genDb.ListEventsAfterParams{MaxRows: 10})
	if err != nil || len(all) != 2 || all[1].ActorType != "agent" || all[0].Seq >= all[1].Seq {
		t.Fatalf("stream order = %+v (%v)", all, err)
	}

	if _, updateErr := pool.Exec(ctx, "UPDATE events SET type = 'tampered'"); updateErr == nil {
		t.Fatal("events could be updated")
	}

	deleted, err := q.DeleteEventsBefore(ctx, time.Now().Add(time.Minute))
	if err != nil || deleted != 2 {
		t.Fatalf("retention deleted %d (%v)", deleted, err)
	}
}

func TestRecordWithoutCallerIsAnonymous(t *testing.T) {
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	if err := events.Record(t.Context(), q, events.Event{Type: events.UserCreated}); err != nil {
		t.Fatalf("record: %v", err)
	}
	authtest.WaitForEarlierTransactions(t, pool)
	rows, err := q.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 1})
	if err != nil || len(rows) != 1 || rows[0].ActorType != events.ActorAnonymous || rows[0].ActorID != nil {
		t.Fatalf("rows = %+v (%v)", rows, err)
	}
}

func TestRecordStoresTheCallersTraceparent(t *testing.T) {
	tracingtest.Record(t)
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	traced := tracingtest.StartParent(t)
	if err := events.Record(traced, q, events.Event{Type: events.ClusterRegistered}); err != nil {
		t.Fatalf("record traced event: %v", err)
	}
	if err := events.Record(t.Context(), q, events.Event{Type: events.OrgUpdated}); err != nil {
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
	pool := authtest.NewPool(t)
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
