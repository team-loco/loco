package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

type eventFixture struct {
	queries *genDb.Queries
	machine *tvm.VendingMachine
	events  *EventServer
	owner   context.Context
	orgID   string
}

func scoped(t *testing.T, machine *tvm.VendingMachine, userID uuid.UUID) context.Context {
	t.Helper()
	scopes, err := machine.UserScopes(t.Context(), userID)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, genDb.Entity{Type: genDb.EntityTypeUser, ID: userID})
	return context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
}

func newEventFixture(t *testing.T) *eventFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	queries := genDb.New(pool)
	machine := tvm.NewVendingMachine(pool, queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	owner, err := auth.NewResolver(pool, policy).Resolve(t.Context(), auth.Identity{
		Issuer: testIssuer, Subject: "owner", Email: "owner@acme.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ownerCtx := scoped(t, machine, owner.ID)
	created, err := NewOrgServer(pool, queries, machine).CreateOrg(ownerCtx, connect.NewRequest(&orgv1.CreateOrgRequest{
		Name: new("acme"),
	}))
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return &eventFixture{
		queries: queries,
		machine: machine,
		events:  NewEventServer(queries, machine),
		owner:   scoped(t, machine, owner.ID),
		orgID:   created.Msg.GetOrgId(),
	}
}

func eventTypes(list []*eventv1.Event) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.GetType()
	}
	return out
}

func TestSignupAndOrgCreationAreRecorded(t *testing.T) {
	f := newEventFixture(t)
	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{Seq: 0, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 || all[0].Type != events.UserCreated || all[1].Type != events.OrgCreated {
		t.Fatalf("events = %+v", all)
	}
	if all[1].OrgID == nil || all[1].OrgID.String() != f.orgID || all[1].ActorType != "user" {
		t.Fatalf("org event = %+v", all[1])
	}
}

func TestListOrgEventsAuthorizationPagingAndFilter(t *testing.T) {
	f := newEventFixture(t)
	orgID := uuid.MustParse(f.orgID)
	for range 5 {
		events.Record(f.owner, f.queries, events.Event{Type: events.OrgUpdated, OrgID: &orgID})
	}

	first, err := f.events.ListOrgEvents(f.owner, connect.NewRequest(&eventv1.ListOrgEventsRequest{
		OrgId: f.orgID, PageSize: 4,
	}))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first.Msg.GetEvents()) != 4 || first.Msg.GetNextBeforeSeq() == 0 {
		t.Fatalf("first page = %v next=%d", eventTypes(first.Msg.GetEvents()), first.Msg.GetNextBeforeSeq())
	}
	second, err := f.events.ListOrgEvents(f.owner, connect.NewRequest(&eventv1.ListOrgEventsRequest{
		OrgId: f.orgID, PageSize: 4, BeforeSeq: first.Msg.GetNextBeforeSeq(),
	}))
	if err != nil || len(second.Msg.GetEvents()) != 2 || second.Msg.GetNextBeforeSeq() != 0 {
		t.Fatalf("second page = %v (%v)", eventTypes(second.Msg.GetEvents()), err)
	}
	if got := second.Msg.GetEvents()[1]; got.GetType() != events.OrgCreated ||
		got.GetData().GetFields()["name"].GetStringValue() != "acme" || got.GetActorEmail() != "owner@acme.test" {
		t.Fatalf("oldest event = %+v", got)
	}

	filtered, err := f.events.ListOrgEvents(f.owner, connect.NewRequest(&eventv1.ListOrgEventsRequest{
		OrgId: f.orgID, Types: []string{events.OrgCreated},
	}))
	if err != nil || len(filtered.Msg.GetEvents()) != 1 {
		t.Fatalf("filtered = %v (%v)", eventTypes(filtered.Msg.GetEvents()), err)
	}

	outsider, err := f.queries.CreateUser(t.Context(), genDb.CreateUserParams{Email: "outsider@acme.test"})
	if err != nil {
		t.Fatalf("outsider: %v", err)
	}
	if grantErr := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: outsider.ID, EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: genDb.ScopeRead,
	}); grantErr != nil {
		t.Fatalf("grant: %v", grantErr)
	}
	_, err = f.events.ListOrgEvents(scoped(t, f.machine, outsider.ID), connect.NewRequest(&eventv1.ListOrgEventsRequest{
		OrgId: f.orgID,
	}))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("reader listed the audit log: %v", err)
	}
}

func TestStreamEventsRequiresSystemAndFollows(t *testing.T) {
	f := newEventFixture(t)
	f.events.poll = 10 * time.Millisecond

	systemScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
	}
	var asSystem atomic.Bool
	asSystem.Store(true)
	mux := http.NewServeMux()
	mux.Handle(
		eventv1connect.NewEventServiceHandler(
			f.events,
			connect.WithInterceptors(scopeInjector(func() []genDb.EntityScope {
				if asSystem.Load() {
					return systemScopes
				}
				return nil
			})),
		),
	)
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	client := eventv1connect.NewEventServiceClient(srv.Client(), srv.URL)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, connect.NewRequest(&eventv1.StreamEventsRequest{}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("no first batch: %v", stream.Err())
	}
	if got := eventTypes(stream.Msg().GetEvents()); len(got) != 2 {
		t.Fatalf("first batch = %v", got)
	}

	events.Record(f.owner, f.queries, events.Event{Type: events.TokenCreated})
	if !stream.Receive() {
		t.Fatalf("no follow-up batch: %v", stream.Err())
	}
	if got := eventTypes(stream.Msg().GetEvents()); len(got) != 1 || got[0] != events.TokenCreated {
		t.Fatalf("follow-up batch = %v", got)
	}
	cancel()

	asSystem.Store(false)
	denied, err := client.StreamEvents(t.Context(), connect.NewRequest(&eventv1.StreamEventsRequest{}))
	if err == nil {
		if denied.Receive() {
			t.Fatal("non-system caller received events")
		}
		err = denied.Err()
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-system stream: %v", err)
	}
}

type scopeInjector func() []genDb.EntityScope

func (scopeInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}

func (scopeInjector) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (s scopeInjector) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		return next(context.WithValue(ctx, contextkeys.EntityScopesKey, s()), conn)
	}
}
