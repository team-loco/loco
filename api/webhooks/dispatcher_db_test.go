package webhooks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/webhooks"
)

const orgName = "acme"

type receiver struct {
	srv      *httptest.Server
	status   atomic.Int32
	mu       sync.Mutex
	payloads []webhooks.Payload
}

func newReceiver(t *testing.T, secret string) *receiver {
	t.Helper()
	verifier, err := newStandardVerifier(secret)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	r := &receiver{}
	r.status.Store(http.StatusNoContent)
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, verifyErr := verifier.Verify(req, maxBody)
		if verifyErr != nil {
			t.Errorf("signature: %v", verifyErr)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var p webhooks.Payload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("payload: %v", err)
		}
		r.mu.Lock()
		r.payloads = append(r.payloads, p)
		r.mu.Unlock()
		w.WriteHeader(int(r.status.Load()))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.payloads))
	for i, p := range r.payloads {
		out[i] = p.Type
	}
	return out
}

type fixture struct {
	pool        *pgxpool.Pool
	queries     *genDb.Queries
	orgID       uuid.UUID
	workspaceID uuid.UUID
	other       uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	user, err := q.CreateUser(t.Context(), genDb.CreateUserParams{Email: "owner@acme.test"})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	org, err := q.CreateOrganization(t.Context(), genDb.CreateOrganizationParams{Name: orgName, CreatedBy: user.ID})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	workspace, err := q.CreateWorkspace(t.Context(), genDb.CreateWorkspaceParams{
		OrgID: org.ID, Name: "storefront", CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	other, err := q.CreateWorkspace(t.Context(), genDb.CreateWorkspaceParams{
		OrgID: org.ID, Name: "billing", CreatedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("other workspace: %v", err)
	}
	return &fixture{pool: pool, queries: q, orgID: org.ID, workspaceID: workspace, other: other}
}

func (f *fixture) webhook(t *testing.T, workspace uuid.UUID, types ...string) (genDb.Webhook, string) {
	t.Helper()
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	if types == nil {
		types = []string{}
	}
	w, err := f.queries.CreateWorkspaceWebhook(t.Context(), genDb.CreateWorkspaceWebhookParams{
		WorkspaceID: workspace, Url: "https://placeholder.test", Secret: secret, EventTypes: types,
	})
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	return w, secret
}

func (f *fixture) record(t *testing.T, workspace uuid.UUID, eventType string) {
	t.Helper()
	if err := events.Record(context.Background(), f.queries, events.Event{
		Type: eventType, WorkspaceID: &workspace, Data: map[string]any{events.FieldName: orgName},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
}

func (f *fixture) setURL(t *testing.T, id uuid.UUID, url string) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "UPDATE webhooks SET url = $1 WHERE id = $2", url, id); err != nil {
		t.Fatalf("set url: %v", err)
	}
}

func (f *fixture) deliveries(t *testing.T, webhookID uuid.UUID) []genDb.ListWebhookDeliveriesRow {
	t.Helper()
	rows, err := f.queries.ListWebhookDeliveries(t.Context(), genDb.ListWebhookDeliveriesParams{
		WebhookID: webhookID, MaxRows: 100,
	})
	if err != nil {
		t.Fatalf("deliveries: %v", err)
	}
	return rows
}

func (f *fixture) makeDue(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(
		t.Context(),
		"UPDATE webhook_deliveries SET next_attempt_at = NOW() - INTERVAL '1 second' WHERE status = 'pending'",
	); err != nil {
		t.Fatalf("make due: %v", err)
	}
}

func TestEventsFanOutToMatchingWebhooks(t *testing.T) {
	f := newFixture(t)
	all, allSecret := f.webhook(t, f.workspaceID)
	filtered, filteredSecret := f.webhook(t, f.workspaceID, events.EnvironmentCreated)
	foreign, foreignSecret := f.webhook(t, f.other)
	everything := newReceiver(t, allSecret)
	onlyEnvironments := newReceiver(t, filteredSecret)
	otherWorkspace := newReceiver(t, foreignSecret)
	f.setURL(t, all.ID, everything.srv.URL)
	f.setURL(t, filtered.ID, onlyEnvironments.srv.URL)
	f.setURL(t, foreign.ID, otherWorkspace.srv.URL)

	f.record(t, f.workspaceID, events.EnvironmentCreated)
	f.record(t, f.workspaceID, events.ResourceCreated)
	f.record(t, f.other, events.ResourceCreated)
	if err := events.Record(
		t.Context(),
		f.queries,
		events.Event{Type: events.OrgUpdated, OrgID: &f.orgID},
	); err != nil {
		t.Fatalf("org event: %v", err)
	}
	if err := events.Record(t.Context(), f.queries, events.Event{Type: events.ClusterRegistered}); err != nil {
		t.Fatalf("org-less event: %v", err)
	}

	if got := len(f.deliveries(t, all.ID)); got != 2 {
		t.Fatalf("all-events webhook deliveries = %d, want 2", got)
	}
	if got := len(f.deliveries(t, filtered.ID)); got != 1 {
		t.Fatalf("filtered webhook deliveries = %d, want 1", got)
	}
	if got := len(f.deliveries(t, foreign.ID)); got != 1 {
		t.Fatalf("other workspace's webhook deliveries = %d, want 1", got)
	}

	d := webhooks.NewDispatcher(f.queries, webhooks.NewClient(true), webhooks.NewClient(true))
	sent, err := d.DispatchDue(t.Context())
	if err != nil || sent != 4 {
		t.Fatalf("dispatch = %d, %v", sent, err)
	}
	if got := onlyEnvironments.types(); len(got) != 1 || got[0] != events.EnvironmentCreated {
		t.Fatalf("filtered receiver got %v", got)
	}
	if got := everything.types(); len(got) != 2 {
		t.Fatalf("all-events receiver got %v", got)
	}
	if got := otherWorkspace.types(); len(got) != 1 || got[0] != events.ResourceCreated {
		t.Fatalf("other workspace's receiver got %v", got)
	}
	everything.mu.Lock()
	p := everything.payloads[0]
	everything.mu.Unlock()
	var data map[string]string
	if err := json.Unmarshal(p.Data, &data); err != nil || data[events.FieldName] != orgName {
		t.Fatalf("payload data = %s (%v)", p.Data, err)
	}
	if p.WorkspaceID == nil || *p.WorkspaceID != f.workspaceID || p.OrgID == nil || *p.OrgID != f.orgID ||
		p.Actor.Type != events.ActorAnonymous || p.Type == "" {
		t.Fatalf("payload = %+v", p)
	}
	for _, row := range f.deliveries(t, all.ID) {
		if row.Status != webhooks.StatusSucceeded || row.DeliveredAt == nil || row.Attempts != 1 {
			t.Fatalf("delivery = %+v", row)
		}
	}
	if again, againErr := d.DispatchDue(t.Context()); againErr != nil || again != 0 {
		t.Fatalf("redelivered %d succeeded deliveries (%v)", again, againErr)
	}
}

func TestFailedDeliveriesRetryThenGiveUp(t *testing.T) {
	f := newFixture(t)
	hook, secret := f.webhook(t, f.workspaceID)
	r := newReceiver(t, secret)
	r.status.Store(http.StatusInternalServerError)
	f.setURL(t, hook.ID, r.srv.URL)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	d := webhooks.NewDispatcher(f.queries, webhooks.NewClient(true), webhooks.NewClient(true))

	if _, err := d.DispatchDue(t.Context()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row := f.deliveries(t, hook.ID)[0]
	if row.Status != webhooks.StatusPending || row.Attempts != 1 || row.LastStatusCode == nil ||
		*row.LastStatusCode != http.StatusInternalServerError || row.LastError == nil {
		t.Fatalf("after one failure = %+v", row)
	}
	if again, againErr := d.DispatchDue(t.Context()); againErr != nil || again != 0 {
		t.Fatalf("retried before its backoff elapsed (%v)", againErr)
	}

	for range 6 {
		f.makeDue(t)
		if _, err := d.DispatchDue(t.Context()); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	row = f.deliveries(t, hook.ID)[0]
	if row.Status != webhooks.StatusFailed || row.Attempts != 7 {
		t.Fatalf("after giving up = %+v", row)
	}
	if got := len(r.types()); got != 7 {
		t.Fatalf("receiver saw %d attempts, want 7", got)
	}

	r.status.Store(http.StatusOK)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	if _, err := d.DispatchDue(t.Context()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if latest := f.deliveries(t, hook.ID)[0]; latest.Status != webhooks.StatusSucceeded {
		t.Fatalf("recovered endpoint delivery = %+v", latest)
	}
}

func TestConcurrentDispatchersDeliverOnce(t *testing.T) {
	f := newFixture(t)
	hook, secret := f.webhook(t, f.workspaceID)
	r := newReceiver(t, secret)
	f.setURL(t, hook.ID, r.srv.URL)
	const total = 30
	for range total {
		f.record(t, f.workspaceID, events.ResourceUpdated)
	}
	var wg sync.WaitGroup
	var dispatched atomic.Int64
	for range 3 {
		wg.Go(func() {
			d := webhooks.NewDispatcher(f.queries, webhooks.NewClient(true), webhooks.NewClient(true))
			n, err := d.DispatchDue(context.Background())
			if err != nil {
				t.Errorf("dispatch: %v", err)
			}
			dispatched.Add(int64(n))
		})
	}
	wg.Wait()
	if got := len(r.types()); got != total || dispatched.Load() != total {
		t.Fatalf("receiver got %d, dispatchers claimed %d, want %d", got, dispatched.Load(), total)
	}
}
