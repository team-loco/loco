package webhooks_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/webhooks"
)

var edgeCluster = map[string]any{events.FieldName: "edge-1", events.FieldRegion: "us-ca-1"}

type ntfyRequest struct {
	query         url.Values
	body          string
	authorization string
	path          string
}

type ntfyServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	status   int
	requests []ntfyRequest
}

func newNtfyServer(t *testing.T) *ntfyServer {
	t.Helper()
	s := &ntfyServer{status: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, ntfyRequest{
			query: r.URL.Query(), body: string(body), authorization: r.Header.Get("Authorization"), path: r.URL.Path,
		})
		w.WriteHeader(s.status)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *ntfyServer) respondWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *ntfyServer) received() []ntfyRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ntfyRequest(nil), s.requests...)
}

func (f *fixture) ntfyDispatcher() *webhooks.Dispatcher {
	cfg := testConfig
	cfg.WebURL = "https://app.loco.test"
	return webhooks.NewDispatcher(
		f.queries,
		f.provider,
		cfg,
		webhooks.NewClient(cfg, false),
		webhooks.NewClient(cfg, true),
	)
}

func (f *fixture) syncNtfy(t *testing.T, server, token string, types ...string) {
	t.Helper()
	f.syncNtfyTopic(t, server, "alerts", token, types...)
}

func (f *fixture) syncNtfyTopic(t *testing.T, server, topic, token string, types ...string) {
	t.Helper()
	cfg, err := webhooks.NewNtfyConfig(server, topic, token, types)
	if err != nil {
		t.Fatal(err)
	}
	if err = webhooks.SyncNtfy(t.Context(), f.pool, f.provider, cfg); err != nil {
		t.Fatalf("sync ntfy: %v", err)
	}
}

func (f *fixture) recordInstall(t *testing.T, eventType string, data map[string]any) {
	t.Helper()
	if err := events.Record(t.Context(), f.queries, events.Event{
		ActorType: events.ActorSystem, Type: eventType, Data: data,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNtfyPublishesEachMatchingEventOnce(t *testing.T) {
	f := newFixture(t)
	server := newNtfyServer(t)
	f.syncNtfy(t, server.srv.URL, "tk_secret")
	f.syncNtfy(t, server.srv.URL, "tk_secret")

	f.recordInstall(t, events.ClusterSilent, edgeCluster)
	f.recordInstall(t, events.ClusterRegistered, map[string]any{events.FieldName: "edge-1"})
	if err := events.Record(t.Context(), f.queries, events.Event{
		ActorType: events.ActorSystem, Type: events.OrgSuspended, OrgID: &f.orgID,
		Data: map[string]any{events.FieldReason: "unpaid"},
	}); err != nil {
		t.Fatal(err)
	}

	dispatcher := f.ntfyDispatcher()
	for range 2 {
		if _, err := dispatcher.DispatchDue(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got := server.received()
	if len(got) != 2 {
		t.Fatalf("published %d notifications, want the cluster.silent and org.suspended ones", len(got))
	}
	byTitle := map[string]ntfyRequest{}
	for _, r := range got {
		byTitle[r.query.Get("title")] = r
	}
	silent, suspended := byTitle["Cluster silent"], byTitle["Organization suspended"]
	if silent.path != "/alerts" {
		t.Errorf("cluster.silent path = %q, want /alerts", silent.path)
	}
	if silent.authorization != "Bearer tk_secret" {
		t.Errorf("cluster.silent authorization = %q, want the bearer token", silent.authorization)
	}
	if silent.query.Get("priority") != "5" {
		t.Errorf("cluster.silent priority = %q, want 5", silent.query.Get("priority"))
	}
	if silent.body != "Cluster edge-1 (us-ca-1) stopped reporting." {
		t.Errorf("cluster.silent message = %q", silent.body)
	}
	if suspended.body != "acme was suspended: unpaid" {
		t.Errorf("org.suspended message = %q", suspended.body)
	}
	wantClick := "https://app.loco.test/org/" + f.orgID.String() + "/audit"
	if suspended.query.Get("click") != wantClick {
		t.Errorf("org.suspended click = %q, want %q", suspended.query.Get("click"), wantClick)
	}
}

func (f *fixture) ntfyDelivery(t *testing.T) (string, int, string) {
	t.Helper()
	var status, lastError string
	var attempts int
	if err := f.pool.QueryRow(t.Context(), `
SELECT status, attempts, COALESCE(last_error, '') FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
WHERE w.kind = 'ntfy'`).Scan(&status, &attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	return status, attempts, lastError
}

func TestNtfyFailureIsShownThenRetriedUntilDelivered(t *testing.T) {
	f := newFixture(t)
	server := newNtfyServer(t)
	server.respondWith(http.StatusServiceUnavailable)
	f.syncNtfy(t, server.srv.URL, "", events.ClusterSilent)
	f.recordInstall(t, events.ClusterSilent, edgeCluster)
	dispatcher := f.ntfyDispatcher()

	if _, err := dispatcher.DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, attempts, lastError := f.ntfyDelivery(t)
	if status != webhooks.StatusPending || attempts != 1 {
		t.Fatalf("delivery = %s after %d attempts, want pending after one", status, attempts)
	}
	if lastError == "" {
		t.Fatal("failed delivery has no last error")
	}
	if got := server.received(); len(got) != 1 || got[0].authorization != "" {
		t.Fatalf("publishes = %+v, want one without an authorization header", got)
	}

	server.respondWith(http.StatusOK)
	if _, err := f.pool.Exec(t.Context(), "UPDATE webhook_deliveries SET next_attempt_at = now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	status, attempts, _ = f.ntfyDelivery(t)
	if status != webhooks.StatusSucceeded || attempts != 2 {
		t.Fatalf("delivery = %s after %d attempts, want succeeded on the second", status, attempts)
	}
	if got := server.received(); len(got) != 2 {
		t.Fatalf("publishes = %d, want the failed one and its retry", len(got))
	}
}

func TestNtfyRetryGoesToTheDestinationConfiguredWhenItIsSent(t *testing.T) {
	f := newFixture(t)
	first, second := newNtfyServer(t), newNtfyServer(t)
	first.respondWith(http.StatusServiceUnavailable)
	f.syncNtfy(t, first.srv.URL, "tk_first", events.ClusterSilent)
	f.recordInstall(t, events.ClusterSilent, edgeCluster)
	dispatcher := f.ntfyDispatcher()
	if _, err := dispatcher.DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}

	f.syncNtfyTopic(t, second.srv.URL, "other", "tk_second", events.ClusterSilent)
	if _, err := f.pool.Exec(t.Context(), "UPDATE webhook_deliveries SET next_attempt_at = now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := second.received()
	if len(got) != 1 {
		t.Fatalf("the new destination received %d publishes, want the retry", len(got))
	}
	if got[0].path != "/other" {
		t.Errorf("retry path = %q, want /other", got[0].path)
	}
	if got[0].authorization != "Bearer tk_second" {
		t.Errorf("retry authorization = %q, want the new token", got[0].authorization)
	}
	if status, attempts, _ := f.ntfyDelivery(t); status != webhooks.StatusSucceeded || attempts != 2 {
		t.Fatalf("delivery = %s after %d attempts, want succeeded on the second", status, attempts)
	}
	if len(first.received()) != 1 {
		t.Fatalf("the old destination received %d publishes, want only the first attempt", len(first.received()))
	}
}

func TestNtfyOffDropsQueuedAndLaterEvents(t *testing.T) {
	f := newFixture(t)
	server := newNtfyServer(t)
	f.syncNtfy(t, server.srv.URL, "", events.ClusterSilent)
	f.recordInstall(t, events.ClusterSilent, edgeCluster)
	if err := webhooks.SyncNtfy(t.Context(), f.pool, f.provider, nil); err != nil {
		t.Fatal(err)
	}
	f.recordInstall(t, events.ClusterSilent, edgeCluster)

	if _, err := f.ntfyDispatcher().DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := server.received(); len(got) != 0 {
		t.Fatalf("publishes after turning ntfy off = %+v, want none", got)
	}
	var targets int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM webhooks WHERE kind = 'ntfy'").
		Scan(&targets); err != nil {
		t.Fatal(err)
	}
	if targets != 0 {
		t.Fatalf("ntfy targets = %d, want none", targets)
	}
}
