package service

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2/connectinprocess"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const auditOwnerEmail = "owner@acme.test"

type eventFixture struct {
	pool    *pgxpool.Pool
	queries *genDb.Queries
	machine *tvm.VendingMachine
	events  *EventServer
	owner   context.Context
	orgID   string
}

func scoped(t *testing.T, q genDb.Querier, userID uuid.UUID) context.Context {
	t.Helper()
	scopes, err := authz.New(nil, q).UserScopes(t.Context(), userID)
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
		Issuer: testIssuer, Subject: "owner", Email: auditOwnerEmail, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ownerCtx := scoped(t, queries, owner.ID)
	created, err := NewOrgServer(
		pool,
		queries,
		machine,
		InvitationConfig{},
	).CreateOrg(ownerCtx, &orgv1.CreateOrgRequest{
		Name: new("acme"),
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return &eventFixture{
		pool:    pool,
		queries: queries,
		machine: machine,
		events:  NewEventServer(queries, machine),
		owner:   scoped(t, queries, owner.ID),
		orgID:   created.GetOrgId(),
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
	authtest.WaitForEarlierTransactions(t, f.pool)
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

func TestListOrganizationEventsAuthorizationPagingAndFilter(t *testing.T) {
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

	first, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{
		OrgId: f.orgID, PageSize: 4,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first.GetEvents()) != 4 || first.GetNextBeforeSeq() == 0 {
		t.Fatalf("first page = %v next=%d", eventTypes(first.GetEvents()), first.GetNextBeforeSeq())
	}
	second, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{
		OrgId: f.orgID, PageSize: 4, BeforeSeq: first.GetNextBeforeSeq(),
	})
	if err != nil || len(second.GetEvents()) != 2 || second.GetNextBeforeSeq() != 0 {
		t.Fatalf("second page = %v (%v)", eventTypes(second.GetEvents()), err)
	}
	if got := second.GetEvents()[1]; got.GetType() != events.OrgCreated ||
		got.GetData().GetFields()["name"].GetStringValue() != "acme" || got.GetActorEmail() != auditOwnerEmail {
		t.Fatalf("oldest event = %+v", got)
	}

	filtered, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{
		OrgId: f.orgID, Types: []string{events.OrgCreated},
	})
	if err != nil || len(filtered.GetEvents()) != 1 {
		t.Fatalf("filtered = %v (%v)", eventTypes(filtered.GetEvents()), err)
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
	_, err = f.events.ListEvents(scoped(t, f.queries, outsider.ID), &eventv1.ListEventsRequest{
		OrgId: f.orgID,
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("reader listed the audit log: %v", err)
	}
}

func (f *eventFixture) streamClient(t *testing.T, asSystem *atomic.Bool) eventv1connect.EventServiceClient {
	t.Helper()
	systemScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin},
	}
	server := connect.NewServer(scopeInjector(func() []genDb.EntityScope {
		if asSystem.Load() {
			return systemScopes
		}
		return nil
	}).Interceptor)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	transport := connectinprocess.New(server)
	return eventv1connect.NewEventServiceClient(connect.NewClient(transport))
}

func TestStreamEventsRequiresSystemAndFollows(t *testing.T) {
	f := newEventFixture(t)
	authtest.WaitForEarlierTransactions(t, f.pool)
	f.events.poll = 10 * time.Millisecond
	var asSystem atomic.Bool
	asSystem.Store(true)
	client := f.streamClient(t, &asSystem)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	streamMessage, receiveErr := stream.Receive()
	if receiveErr != nil {
		t.Fatalf("no first batch: %v", receiveErr)
	}
	if got := eventTypes(streamMessage.GetEvents()); len(got) != 2 {
		t.Fatalf("first batch = %v", got)
	}

	if recordErr := events.Record(f.owner, f.queries, events.Event{Type: events.TokenCreated}); recordErr != nil {
		t.Fatalf("record: %v", recordErr)
	}
	streamMessage, receiveErr = stream.Receive()
	if receiveErr != nil {
		t.Fatalf("no follow-up batch: %v", receiveErr)
	}
	if got := eventTypes(streamMessage.GetEvents()); len(got) != 1 || got[0] != events.TokenCreated {
		t.Fatalf("follow-up batch = %v", got)
	}
	cancel()

	asSystem.Store(false)
	denied, err := client.StreamEvents(t.Context(), &eventv1.StreamEventsRequest{})
	if err == nil {
		_, err = denied.Receive()
		if err == nil {
			t.Fatal("non-system caller received events")
		}
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-system stream: %v", err)
	}
}

type streamedBatch struct {
	types  []string
	cursor string
}

func receiveBatches(stream eventv1connect.EventServiceStreamEventsClientStream) <-chan streamedBatch {
	out := make(chan streamedBatch)
	go func() {
		defer close(out)
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			out <- streamedBatch{types: eventTypes(msg.GetEvents()), cursor: msg.GetCursor()}
		}
	}()
	return out
}

func TestStreamEventsWaitsForEarlierTransactions(t *testing.T) {
	f := newEventFixture(t)
	authtest.WaitForEarlierTransactions(t, f.pool)
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

	stream, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
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
			t.Fatalf("stream ended after %v", delivered)
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
	resumed, err := client.StreamEvents(resumeCtx, &eventv1.StreamEventsRequest{Cursor: cursor})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumedMessage, receiveErr := resumed.Receive()
	if receiveErr != nil {
		t.Fatalf("no batch after resume: %v", receiveErr)
	}
	if got := eventTypes(resumedMessage.GetEvents()); !slices.Equal(got, []string{events.WorkspaceDeleted}) {
		t.Fatalf("resumed batch = %v", got)
	}

	bad, err := client.StreamEvents(
		t.Context(),
		&eventv1.StreamEventsRequest{Cursor: "not-a-cursor"},
	)
	if err == nil {
		_, err = bad.Receive()
		if err == nil {
			t.Fatal("malformed cursor streamed events")
		}
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
	orgs := NewOrgServer(f.pool, f.queries, f.machine, InvitationConfig{})
	orgID := uuid.MustParse(f.orgID)
	if _, err := f.pool.Exec(t.Context(), failEventInsertsSQL); err != nil {
		t.Fatalf("install failing trigger: %v", err)
	}

	_, err := orgs.UpdateOrg(f.owner, &orgv1.UpdateOrgRequest{OrgId: f.orgID, Name: new("renamed")})
	if err == nil {
		t.Fatal("update succeeded without its event")
	}
	org, err := f.queries.GetOrganizationByID(t.Context(), orgID)
	if err != nil || org.Name != "acme" {
		t.Fatalf("org after failed update = %q (%v)", org.Name, err)
	}

	_, err = orgs.DeleteOrg(f.owner, &orgv1.DeleteOrgRequest{OrgId: f.orgID})
	if err == nil {
		t.Fatal("delete succeeded without its event")
	}
	if _, getErr := f.queries.GetOrganizationByID(t.Context(), orgID); getErr != nil {
		t.Fatalf("org after failed delete: %v", getErr)
	}
}

type scopeInjector func() []genDb.EntityScope

func (s scopeInjector) Interceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		return next(context.WithValue(ctx, contextkeys.EntityScopesKey, s()), spec, stream)
	}
}
