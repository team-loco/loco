package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

// streamOpenLimit is half an idle tail's first heartbeat, at about 6 s.
const streamOpenLimit = 3 * time.Second

func TestIdleTailLogsOpensAtOnce(t *testing.T) {
	t.Parallel()
	f := newObservabilityFixture(t)
	server := NewObservabilityServer(newTestClickHouse(t), f.authorizer, f.queries, observabilityTestConfig)
	ctx := callerContext(t, workspaceReader)
	requireHeadersWithin(t, observabilityTestHandler(server, f.callers), func(transport connect.Transport) error {
		client := observabilityv1connect.NewObservabilityServiceClient(connect.NewClient(transport))
		_, err := client.TailLogs(ctx, &observabilityv1.TailLogsRequest{WorkspaceId: f.workspace.String()})
		return err
	})
}

func TestIdleStreamEventsOpensAtOnce(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	system := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin}}
	server := connect.NewServer(scopeInjector(func() []genDb.EntityScope { return system }).Interceptor)
	eventv1connect.RegisterEventServiceHandler(server, f.events)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	ctx := t.Context()
	requireHeadersWithin(t, mux, func(transport connect.Transport) error {
		client := eventv1connect.NewEventServiceClient(connect.NewClient(transport))
		// The fixture's events are unpublished, so the stream has nothing to send.
		_, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
		return err
	})
}

// requireHeadersWithin serves handler, opens a stream on it with open, and fails unless the
// response headers reach the client within streamOpenLimit. The stream stays open until the test
// context ends.
func requireHeadersWithin(t *testing.T, handler http.Handler, open func(connect.Transport) error) {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	headers := make(chan struct{}, 1)
	client := httpServer.Client()
	client.Transport = headerSignal{base: client.Transport, headers: headers}
	failed := make(chan error, 1)
	go func() { failed <- open(connecthttp.NewTransport(client, httpServer.URL)) }()
	timeout := time.After(streamOpenLimit)
	for {
		select {
		case <-headers:
			return
		case err := <-failed:
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}
			failed = nil
		case <-timeout:
			t.Fatalf("no response headers within %v", streamOpenLimit)
		}
	}
}

// headerSignal reports each response whose headers arrived.
type headerSignal struct {
	base    http.RoundTripper
	headers chan<- struct{}
}

func (s headerSignal) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(req)
	if err == nil {
		s.headers <- struct{}{}
	}
	return resp, err
}
