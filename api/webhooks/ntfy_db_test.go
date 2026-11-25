package webhooks_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/team-loco/loco/api/events"
	"github.com/team-loco/loco/api/webhooks"
)

var edgeCluster = map[string]any{events.FieldName: "edge-1", events.FieldRegion: "us-ca-1"}

type ntfyRequest struct {
	query         map[string][]string
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
	cfg, err := webhooks.NewNtfyConfig(server, "alerts", token, types)
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
	silent, suspended := got[0], got[1]
	if silent.path != "/alerts" || silent.authorization != "Bearer tk_secret" ||
		silent.query["title"][0] != "Cluster silent" || silent.query["priority"][0] != "5" ||
		silent.body != "Cluster edge-1 (us-ca-1) stopped reporting." {
		t.Errorf("cluster.silent publish = %+v", silent)
	}
	if suspended.body != "acme was suspended: unpaid" ||
		suspended.query["click"][0] != "https://app.loco.test/org/"+f.orgID.String()+"/audit" {
		t.Errorf("org.suspended publish = %+v", suspended)
	}
}

func TestNtfyFailureIsRetriedAndShownOnTheDelivery(t *testing.T) {
	f := newFixture(t)
	server := newNtfyServer(t)
	server.status = http.StatusServiceUnavailable
	f.syncNtfy(t, server.srv.URL, "", events.ClusterSilent)
	f.recordInstall(t, events.ClusterSilent, edgeCluster)

	if _, err := f.ntfyDispatcher().DispatchDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	var status, lastError string
	var attempts int
	if err := f.pool.QueryRow(t.Context(), `
SELECT status, attempts, COALESCE(last_error, '') FROM webhook_deliveries d JOIN webhooks w ON w.id = d.webhook_id
WHERE w.kind = 'ntfy'`).Scan(&status, &attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != webhooks.StatusPending || attempts != 1 || lastError == "" {
		t.Fatalf(
			"delivery = %s after %d attempts with error %q, want pending after one failed attempt",
			status,
			attempts,
			lastError,
		)
	}
	if got := server.received(); len(got) != 1 || got[0].authorization != "" {
		t.Fatalf("publishes = %+v, want one without an authorization header", got)
	}
}

func TestNtfyOffRemovesTheTarget(t *testing.T) {
	f := newFixture(t)
	f.syncNtfy(t, newNtfyServer(t).srv.URL, "")
	if err := webhooks.SyncNtfy(t.Context(), f.pool, f.provider, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM webhooks WHERE kind = 'ntfy'").
		Scan(&n); err != nil ||
		n != 0 {
		t.Fatalf("ntfy targets = %d, err %v, want none", n, err)
	}
}
