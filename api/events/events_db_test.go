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
	if recordErr := events.RecordWith(reqCtx, q, events.Event{
		Type:        events.EnvironmentCreated,
		WorkspaceID: new(wsID),
		SubjectType: "environment",
		SubjectID:   new(uuid.New()),
		Data:        map[string]any{"name": "staging"},
	}); recordErr != nil {
		t.Fatalf("record: %v", recordErr)
	}
	events.Record(ctx, q, events.Event{Type: events.ClusterRegistered, ActorType: "agent"})

	rows, err := q.ListOrgEvents(ctx, genDb.ListOrgEventsParams{OrgID: &org.ID, Types: []string{}, MaxRows: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("org events = %d (%v)", len(rows), err)
	}
	ev := rows[0]
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

	all, err := q.ListEventsAfter(ctx, genDb.ListEventsAfterParams{Seq: 0, Limit: 10})
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
	if err := events.RecordWith(t.Context(), q, events.Event{Type: events.UserCreated}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := q.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{Seq: 0, Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ActorType != events.ActorAnonymous || rows[0].ActorID != nil {
		t.Fatalf("rows = %+v (%v)", rows, err)
	}
}
