package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool    *pgxpool.Pool
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
		pool:    pool,
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
	all, err := f.queries.ListEventsAfter(t.Context(), genDb.ListEventsAfterParams{MaxRows: 10})
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
		if recordErr := events.Record(
			f.owner,
			f.queries,
			events.Event{Type: events.OrgUpdated, OrgID: &orgID},
		); recordErr != nil {
			t.Fatalf("record: %v", recordErr)
		}
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
		got.GetData().GetFields()["name"].GetStringValue() != "acme" {
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

func (f *eventFixture) streamClient(t *testing.T, asSystem *atomic.Bool) eventv1connect.EventServiceClient {
	t.Helper()
	systemScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
	}
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
	return eventv1connect.NewEventServiceClient(srv.Client(), srv.URL)
}

func TestStreamEventsRequiresSystemAndFollows(t *testing.T) {
	f := newEventFixture(t)
	f.events.poll = 10 * time.Millisecond
	var asSystem atomic.Bool
	asSystem.Store(true)
	client := f.streamClient(t, &asSystem)

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

	if recordErr := events.Record(f.owner, f.queries, events.Event{Type: events.TokenCreated}); recordErr != nil {
		t.Fatalf("record: %v", recordErr)
	}
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

type streamedBatch struct {
	types  []string
	cursor string
}

func receiveBatches(stream *connect.ServerStreamForClient[eventv1.StreamEventsResponse]) <-chan streamedBatch {
	out := make(chan streamedBatch)
	go func() {
		defer close(out)
		for stream.Receive() {
			msg := stream.Msg()
			out <- streamedBatch{types: eventTypes(msg.GetEvents()), cursor: msg.GetCursor()}
		}
	}()
	return out
}

func TestStreamEventsWaitsForEarlierTransactions(t *testing.T) {
	f := newEventFixture(t)
	f.events.poll = 10 * time.Millisecond
	var asSystem atomic.Bool
	asSystem.Store(true)
	client := f.streamClient(t, &asSystem)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	open, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if rollbackErr := open.Rollback(
			context.Background(),
		); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()
	if recordErr := events.Record(ctx, genDb.New(open), events.Event{Type: events.WorkspaceCreated}); recordErr != nil {
		t.Fatalf("record in open transaction: %v", recordErr)
	}
	if recordErr := events.Record(ctx, f.queries, events.Event{Type: events.WorkspaceUpdated}); recordErr != nil {
		t.Fatalf("record committed: %v", recordErr)
	}

	stream, err := client.StreamEvents(ctx, connect.NewRequest(&eventv1.StreamEventsRequest{}))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	batches := receiveBatches(stream)
	var delivered []string
	first := <-batches
	delivered = append(delivered, first.types...)
	if want := []string{events.UserCreated, events.OrgCreated}; !slices.Equal(delivered, want) {
		t.Fatalf("delivered while a transaction was open = %v, want %v", delivered, want)
	}
	select {
	case early := <-batches:
		t.Fatalf("delivered %v past a transaction that is still open", early.types)
	case <-time.After(20 * f.events.poll):
	}

	if commitErr := open.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}
	cursor := first.cursor
	want := []string{events.UserCreated, events.OrgCreated, events.WorkspaceCreated, events.WorkspaceUpdated}
	for len(delivered) < len(want) {
		next, ok := <-batches
		if !ok {
			t.Fatalf("stream ended after %v: %v", delivered, stream.Err())
		}
		delivered = append(delivered, next.types...)
		cursor = next.cursor
	}
	if !slices.Equal(delivered, want) {
		t.Fatalf("delivered %v, want %v", delivered, want)
	}
	cancel()

	if recordErr := events.Record(
		t.Context(),
		f.queries,
		events.Event{Type: events.WorkspaceDeleted},
	); recordErr != nil {
		t.Fatalf("record after reconnect: %v", recordErr)
	}
	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer resumeCancel()
	resumed, err := client.StreamEvents(resumeCtx, connect.NewRequest(&eventv1.StreamEventsRequest{Cursor: cursor}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed.Receive() {
		t.Fatalf("no batch after resume: %v", resumed.Err())
	}
	if got := eventTypes(resumed.Msg().GetEvents()); !slices.Equal(got, []string{events.WorkspaceDeleted}) {
		t.Fatalf("resumed batch = %v", got)
	}

	bad, err := client.StreamEvents(
		t.Context(),
		connect.NewRequest(&eventv1.StreamEventsRequest{Cursor: "not-a-cursor"}),
	)
	if err == nil {
		if bad.Receive() {
			t.Fatal("malformed cursor streamed events")
		}
		err = bad.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed cursor: %v", err)
	}
}

const failEventInsertsSQL = `
CREATE FUNCTION fail_event_insert() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'event store unavailable';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER fail_event_insert BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION fail_event_insert();
`

func TestMutationFailsWithoutItsEvent(t *testing.T) {
	f := newEventFixture(t)
	orgs := NewOrgServer(f.pool, f.queries, f.machine)
	orgID := uuid.MustParse(f.orgID)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}

	_, err := orgs.UpdateOrg(f.owner, connect.NewRequest(&orgv1.UpdateOrgRequest{OrgId: f.orgID, Name: new("renamed")}))
	if err == nil {
		t.Fatal("update succeeded without its event")
	}
	org, err := f.queries.GetOrganizationByID(t.Context(), orgID)
	if err != nil || org.Name != "acme" {
		t.Fatalf("org after failed update = %q (%v)", org.Name, err)
	}

	_, err = orgs.DeleteOrg(f.owner, connect.NewRequest(&orgv1.DeleteOrgRequest{OrgId: f.orgID}))
	if err == nil {
		t.Fatal("delete succeeded without its event")
	}
	if _, getErr := f.queries.GetOrganizationByID(t.Context(), orgID); getErr != nil {
		t.Fatalf("org after failed delete: %v", getErr)
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
