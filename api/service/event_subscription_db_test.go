package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
)

func TestEventSubscriptionsMatchListedScope(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	f.events.poll = time.Millisecond
	publishEvents(t, f.pool)
	org := uuid.MustParse(f.orgID)
	for _, tc := range []struct {
		name    string
		entity  genDb.EntityType
		id      uuid.UUID
		scope   genDb.Scope
		request *eventv1.StreamEventsRequest
	}{
		{"organization audit", genDb.EntityTypeOrganization, org, genDb.ScopeAdmin,
			&eventv1.StreamEventsRequest{OrgId: f.orgID}},
		{"organization reader denied", genDb.EntityTypeOrganization, org, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID}},
		{"installation audit", genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{InstallationOnly: true}},
		{"installation filtered by type", genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{Types: []string{"resource.updated"}}},
		{"operator organization audit", genDb.EntityTypeSystem, uuid.Nil, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID}},
		{"workspace audit", genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeAdmin,
			&eventv1.StreamEventsRequest{OrgId: f.orgID, WorkspaceId: f.workspaceID.String()}},
		{"workspace reader resources", genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID, WorkspaceId: f.workspaceID.String(), ReadableResources: true}},
		{"single resource grant", genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID, ResourceId: f.resourceID.String()}},
		{"resource reader workspace subset", genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID, WorkspaceId: f.workspaceID.String(), ReadableResources: true,
				ResourceIds: []string{f.resourceID.String(), f.otherResourceID.String()}, Types: []string{"resource.updated"}}},
		{"resource reader workspace audit denied", genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.orgID, WorkspaceId: f.workspaceID.String()}},
		{"wrong organization denied", genDb.EntityTypeResource, f.resourceID, genDb.ScopeRead,
			&eventv1.StreamEventsRequest{OrgId: f.otherOrgID.String(), ResourceId: f.resourceID.String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := auditScope(t, tc.entity, tc.id, tc.scope)
			r := tc.request
			listed, listErr := f.events.ListEvents(caller, &eventv1.ListEventsRequest{
				OrgId: r.GetOrgId(), WorkspaceId: r.GetWorkspaceId(), ResourceId: r.GetResourceId(),
				InstallationOnly: r.GetInstallationOnly(), ReadableResources: r.GetReadableResources(),
				ResourceIds: r.GetResourceIds(), Types: r.GetTypes(),
			})
			principal, _ := reqctx.PrincipalFrom(caller)
			server := connect.NewServer(eventPrincipalInjector{principal}.Interceptor)
			eventv1connect.RegisterEventServiceHandler(server, f.events)
			client := eventv1connect.NewEventServiceClient(connect.NewClient(connectinprocess.New(server)))
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			stream, err := client.StreamEvents(ctx, r)
			var batch *eventv1.StreamEventsResponse
			if err == nil {
				defer stream.Close()
				batch, err = stream.Receive()
			}
			if listErr != nil {
				if connect.CodeOf(err) != connect.CodeOf(listErr) {
					t.Fatalf("stream error = %v, list error = %v", err, listErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(batch.GetEvents()) != len(listed.GetEvents()) {
				t.Fatalf("stream has %d events, list has %d", len(batch.GetEvents()), len(listed.GetEvents()))
			}
			for i, event := range batch.GetEvents() {
				want := listed.GetEvents()[len(listed.GetEvents())-i-1]
				if !proto.Equal(event, want) {
					t.Fatalf("stream event = %v, listed event = %v", event, want)
				}
			}
			t.Logf(
				"scope=%s list=%d stream=%d cursor=%s",
				tc.name,
				len(listed.GetEvents()),
				len(batch.GetEvents()),
				batch.GetCursor(),
			)
		})
	}
}

func TestReadableEventSubscriptionDropsARevokedGrant(t *testing.T) {
	t.Parallel()
	f := newScopedAuditFixture(t)
	f.events.poll = time.Millisecond
	publishEvents(t, f.pool)
	var removed atomic.Bool
	checked := make(chan struct{}, 2)
	scopes := func() []genDb.EntityScope {
		grants := []genDb.EntityScope{
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
		}
		if !removed.Load() {
			grants = append(
				grants,
				genDb.EntityScope{
					EntityType: genDb.EntityTypeResource,
					EntityID:   f.otherResourceID,
					Scope:      genDb.ScopeRead,
				},
			)
		}
		if removed.Load() {
			select {
			case checked <- struct{}{}:
			default:
			}
		}
		return grants
	}
	server := connect.NewServer(scopeInjector(scopes).Interceptor)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	client := eventv1connect.NewEventServiceClient(connect.NewClient(connectinprocess.New(server)))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{
		OrgId: f.orgID, WorkspaceId: f.workspaceID.String(), ReadableResources: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	first, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.GetEvents()) != 10 {
		t.Fatalf("initial subscription = %v", first)
	}
	removed.Store(true)
	// The second sequential validation read proves the first authorization update completed.
	for range 2 {
		select {
		case <-checked:
		case <-ctx.Done():
			t.Fatal("access was not rechecked")
		}
	}
	for _, resource := range []uuid.UUID{f.otherResourceID, f.resourceID} {
		if recordErr := events.Record(
			f.owner,
			f.queries,
			events.Event{Type: events.ResourceUpdated, ResourceID: &resource},
		); recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	publishEvents(t, f.pool)
	next, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.GetEvents()) != 1 || next.GetEvents()[0].GetResourceId() != f.resourceID.String() {
		t.Fatalf("events after partial revocation = %v", next)
	}
}

func TestEventArrivalCountUsesCursorBeyondCappedHistory(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	f.events.poll = time.Millisecond
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO events(type,org_id,actor_type)
SELECT 'resource.updated',$1,'system' FROM generate_series(1,$2::int)`, f.orgID, totalCountCap+1); err != nil {
		t.Fatal(err)
	}
	late, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		rollbackErr := late.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Error(rollbackErr)
		}
	})
	org := uuid.MustParse(f.orgID)
	if err = events.Record(
		t.Context(),
		genDb.New(late),
		events.Event{Type: events.ResourceUpdated, OrgID: &org, ActorType: events.ActorSystem},
	); err != nil {
		t.Fatal(err)
	}
	if err = events.Record(
		t.Context(),
		f.queries,
		events.Event{Type: events.ResourceUpdated, OrgID: &org, ActorType: events.ActorSystem},
	); err != nil {
		t.Fatal(err)
	}
	for range (totalCountCap+2)/streamBatch + 1 {
		publishEvents(t, f.pool)
	}
	principal, _ := reqctx.PrincipalFrom(f.owner)
	server := connect.NewServer(eventPrincipalInjector{principal}.Interceptor)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	client := eventv1connect.NewEventServiceClient(connect.NewClient(connectinprocess.New(server)))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(
		ctx,
		&eventv1.StreamEventsRequest{OrgId: f.orgID, Types: []string{events.ResourceUpdated}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var cursor string
	for received := 0; received < totalCountCap+2; {
		batch, receiveErr := stream.Receive()
		if receiveErr != nil {
			t.Fatal(receiveErr)
		}
		received += len(batch.GetEvents())
		cursor = batch.GetCursor()
	}
	request := &eventv1.ListEventsRequest{
		OrgId:             f.orgID,
		Types:             []string{events.ResourceUpdated},
		IncludeTotalCount: true,
		PageSize:          1,
	}
	history, err := f.events.ListEvents(f.owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if history.GetTotalCount() != totalCountCap+1 {
		t.Fatalf("history count = %d", history.GetTotalCount())
	}
	request.AfterCursor = cursor
	before, err := f.events.ListEvents(f.owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if before.GetTotalCount() != 0 {
		t.Fatalf("new arrivals before commit = %v", before)
	}
	if err = late.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	publishEvents(t, f.pool)
	after, err := f.events.ListEvents(f.owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if after.GetTotalCount() != 1 {
		t.Fatalf("late arrival after capped history = %v", after)
	}
	request.AfterCursor = "malformed-arrival-cursor"
	if _, err = f.events.ListEvents(f.owner, request); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed arrival cursor = %v", err)
	}
}
