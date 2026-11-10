package webhooks_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
	"github.com/team-loco/loco/api/webhooks"
)

const (
	orgName        = "acme"
	transitTimeout = 2 * time.Second
	dekCacheTTL    = time.Minute
)

type receiver struct {
	srv          *httptest.Server
	status       atomic.Int32
	mu           sync.Mutex
	payloads     []webhooks.Payload
	traceparents []string
}

func newReceiver(t *testing.T, secret string) *receiver {
	t.Helper()
	verifier, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	r := &receiver{}
	r.status.Store(http.StatusNoContent)
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Errorf("read body: %v", readErr)
		}
		if verifyErr := verifier.Verify(body, req.Header); verifyErr != nil {
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
		r.traceparents = append(r.traceparents, req.Header.Get("Traceparent"))
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
	provider    secretkeys.Provider
	orgID       uuid.UUID
	workspaceID uuid.UUID
	other       uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.NewPool(t)
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
	return &fixture{
		pool:        pool,
		queries:     q,
		provider:    newKeyProvider(t),
		orgID:       org.ID,
		workspaceID: workspace,
		other:       other,
	}
}

// newKeyProvider returns the provider the API runs with, against a fake Transit server.
func newKeyProvider(t *testing.T) *secretkeys.DEKCache {
	t.Helper()
	server := transittest.New(t).Server()
	client, err := transit.New(t.Context(), transit.Config{
		Address:        server.URL,
		Mount:          transittest.Mount,
		Auth:           transit.AuthToken,
		Token:          transittest.Token,
		Timeout:        transitTimeout,
		StartupTimeout: transitTimeout,
		RenewMargin:    time.Second,
		RenewRetry:     time.Second,
	})
	if err != nil {
		t.Fatalf("transit client: %v", err)
	}
	provider, err := secretkeys.New(t.Context(), client, secretkeys.Config{Key: transittest.Key, CacheTTL: dekCacheTTL})
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return provider
}

func (f *fixture) dispatcher(workspacePrivate bool) *webhooks.Dispatcher {
	workspace := webhooks.NewClient(testConfig, workspacePrivate)
	return webhooks.NewDispatcher(f.queries, f.provider, testConfig, workspace, webhooks.NewClient(testConfig, true))
}

// stored returns webhook id's data key and sealed secrets as the database holds them.
func (f *fixture) stored(t *testing.T, id uuid.UUID) (secretkeys.WrappedKey, []byte, []byte) {
	t.Helper()
	var key secretkeys.WrappedKey
	var secret, previous []byte
	if err := f.pool.QueryRow(
		t.Context(),
		"SELECT provider, kek_id, wrapped_dek, secret, previous_secret FROM webhooks WHERE id = $1",
		id,
	).Scan(&key.Provider, &key.KeyID, &key.Bytes, &secret, &previous); err != nil {
		t.Fatalf("stored secrets: %v", err)
	}
	return key, secret, previous
}

// assertSealed fails when a stored column holds secret or the key bytes it encodes.
func assertSealed(t *testing.T, stored []byte, secret string) {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	if len(stored) == 0 || bytes.Contains(stored, []byte(secret)) || bytes.Contains(stored, key) {
		t.Fatalf("stored secret column %x holds the plain secret %q", stored, secret)
	}
}

func (f *fixture) webhook(t *testing.T, workspace uuid.UUID, types ...string) (genDb.Webhook, string) {
	t.Helper()
	secret := newSecret(t)
	if types == nil {
		types = []string{}
	}
	id := uuid.Must(uuid.NewV7())
	key, sealed, err := webhooks.SealSecretWithNewKey(t.Context(), f.provider, id, secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	w, err := f.queries.CreateWorkspaceWebhook(t.Context(), genDb.CreateWorkspaceWebhookParams{
		ID:          id,
		WorkspaceID: workspace,
		Url:         "https://placeholder.test",
		Provider:    key.Provider,
		KekID:       key.KeyID,
		WrappedDek:  key.Bytes,
		Secret:      sealed,
		EventTypes:  types,
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

	d := f.dispatcher(true)
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
	d := f.dispatcher(true)

	if _, err := d.DispatchDue(t.Context()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row := f.deliveries(t, hook.ID)[0]
	if row.Status != webhooks.StatusPending || row.Attempts != 1 || row.LastError == nil ||
		*row.LastError != "endpoint returned 500 Internal Server Error" {
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
			d := f.dispatcher(true)
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

func TestStaleHolderCannotFinishReclaimedDelivery(t *testing.T) {
	f := newFixture(t)
	hook, _ := f.webhook(t, f.workspaceID)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	claim := func() genDb.ClaimWebhookDeliveriesRow {
		t.Helper()
		f.makeDue(t)
		claimed, err := f.queries.ClaimWebhookDeliveries(t.Context(), genDb.ClaimWebhookDeliveriesParams{
			LeaseSeconds: 1, MaxRows: 1,
		})
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim = %d, %v", len(claimed), err)
		}
		return claimed[0]
	}
	stale := claim()
	current := claim()
	if current.Attempts != stale.Attempts+1 {
		t.Fatalf("attempts = %d then %d", stale.Attempts, current.Attempts)
	}
	before := f.deliveries(t, hook.ID)[0]

	rows, err := f.queries.FinishWebhookDelivery(t.Context(), genDb.FinishWebhookDeliveryParams{
		ID: stale.ID, Attempts: stale.Attempts, Status: webhooks.StatusSucceeded, NextAttemptAt: time.Now(),
	})
	if err != nil || rows != 0 {
		t.Fatalf("stale finish affected %d rows (%v)", rows, err)
	}
	if after := f.deliveries(t, hook.ID)[0]; after != before {
		t.Fatalf("stale finish changed the row: %+v -> %+v", before, after)
	}

	rows, err = f.queries.FinishWebhookDelivery(t.Context(), genDb.FinishWebhookDeliveryParams{
		ID: current.ID, Attempts: current.Attempts, Status: webhooks.StatusSucceeded, NextAttemptAt: time.Now(),
	})
	if err != nil || rows != 1 {
		t.Fatalf("current finish affected %d rows (%v)", rows, err)
	}
}

func TestOutcomeIsRecordedAfterShutdown(t *testing.T) {
	f := newFixture(t)
	hook, _ := f.webhook(t, f.workspaceID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	f.setURL(t, hook.ID, srv.URL)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	d := f.dispatcher(true)

	if _, err := d.DispatchDue(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row := f.deliveries(t, hook.ID)[0]
	if row.Status == webhooks.StatusPending && row.LastError == nil {
		t.Fatalf("outcome not recorded, delivery stays leased: %+v", row)
	}
}

func TestDeliveriesCarryThePreviousSecretUntilItExpires(t *testing.T) {
	f := newFixture(t)
	hook, previous := f.webhook(t, f.workspaceID)
	current := newSecret(t)
	key, _, _ := f.stored(t, hook.ID)
	sealed, err := webhooks.SealSecret(t.Context(), f.provider, hook.ID, key, current)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err = f.queries.RotateWorkspaceWebhookSecret(t.Context(), genDb.RotateWorkspaceWebhookSecretParams{
		ID: hook.ID, WorkspaceID: f.workspaceID, Secret: sealed, GraceSeconds: 3600,
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	_, storedCurrent, storedPrevious := f.stored(t, hook.ID)
	assertSealed(t, storedCurrent, current)
	assertSealed(t, storedPrevious, previous)
	type request struct {
		header http.Header
		body   []byte
	}
	requests := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Errorf("read body: %v", readErr)
		}
		requests <- request{header: req.Header.Clone(), body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	f.setURL(t, hook.ID, srv.URL)
	d := f.dispatcher(true)
	verifies := func(secret string, got request) bool {
		t.Helper()
		verifier, verifierErr := standardwebhooks.NewWebhook(secret)
		if verifierErr != nil {
			t.Fatalf("verifier: %v", verifierErr)
		}
		return verifier.Verify(got.body, got.header) == nil
	}
	deliver := func(expiresIn string) request {
		t.Helper()
		if _, execErr := f.pool.Exec(t.Context(),
			"UPDATE webhooks SET previous_secret_expires_at = NOW() + $1::interval WHERE id = $2",
			expiresIn, hook.ID); execErr != nil {
			t.Fatalf("set expiry: %v", execErr)
		}
		f.record(t, f.workspaceID, events.ResourceUpdated)
		if _, dispatchErr := d.DispatchDue(t.Context()); dispatchErr != nil {
			t.Fatalf("dispatch: %v", dispatchErr)
		}
		return <-requests
	}

	during := deliver("1 hour")
	if !verifies(current, during) || !verifies(previous, during) {
		t.Fatalf("delivery during the grace period = %v, want signatures for both secrets",
			during.header.Values(standardwebhooks.HeaderWebhookSignature))
	}
	after := deliver("-1 second")
	if !verifies(current, after) || verifies(previous, after) {
		t.Fatalf("delivery after the grace period = %v, want only the current secret's signature",
			after.header.Values(standardwebhooks.HeaderWebhookSignature))
	}
}

// unavailableProvider seals through the wrapped provider but cannot unwrap, like a sealed Transit.
type unavailableProvider struct {
	secretkeys.Provider
}

var errProviderDown = errors.New("transit is unavailable")

func (unavailableProvider) Unwrap(context.Context, secretkeys.WrappedKey, []byte) ([]byte, error) {
	return nil, errProviderDown
}

func TestDeliveriesWaitOutAKeyProviderOutage(t *testing.T) {
	f := newFixture(t)
	hook, secret := f.webhook(t, f.workspaceID)
	r := newReceiver(t, secret)
	f.setURL(t, hook.ID, r.srv.URL)
	f.record(t, f.workspaceID, events.ResourceUpdated)
	down := webhooks.NewDispatcher(
		f.queries, unavailableProvider{f.provider}, testConfig,
		webhooks.NewClient(testConfig, true), webhooks.NewClient(testConfig, true),
	)

	for range len(testConfig.RetryDelays) + 2 {
		f.makeDue(t)
		if _, err := down.DispatchDue(t.Context()); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
	}
	row := f.deliveries(t, hook.ID)[0]
	if row.Status != webhooks.StatusPending || row.Attempts != 0 || row.LastError == nil ||
		strings.Contains(*row.LastError, errProviderDown.Error()) || time.Until(row.NextAttemptAt) <= 0 {
		t.Fatalf("delivery during the outage = %+v", row)
	}
	if got := len(r.types()); got != 0 {
		t.Fatalf("receiver got %d deliveries during the outage", got)
	}

	f.makeDue(t)
	if _, err := f.dispatcher(true).DispatchDue(t.Context()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if row = f.deliveries(t, hook.ID)[0]; row.Status != webhooks.StatusSucceeded || row.Attempts != 1 {
		t.Fatalf("delivery after the outage = %+v", row)
	}
}

func TestASecretThatDoesNotOpenUsesAnAttempt(t *testing.T) {
	f := newFixture(t)
	hook, _ := f.webhook(t, f.workspaceID)
	if _, err := f.pool.Exec(t.Context(),
		"UPDATE webhooks SET secret = set_byte(secret, 20, get_byte(secret, 20) # 1) WHERE id = $1",
		hook.ID); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}
	f.record(t, f.workspaceID, events.ResourceUpdated)
	if _, err := f.dispatcher(true).DispatchDue(t.Context()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row := f.deliveries(t, hook.ID)[0]
	if row.Status != webhooks.StatusPending || row.Attempts != 1 || row.LastError == nil ||
		*row.LastError != webhooks.ErrSecretUnusable.Error() {
		t.Fatalf("delivery with a corrupt secret = %+v", row)
	}
}
