package service

import (
	"context"
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
)

// streamOpenLimit is well under each stream's first poll interval. A client's call returns once
// the response headers arrive, so a stream that sends them only with its first message misses it.
const streamOpenLimit = time.Second

func TestIdleTailLogsOpensAtOnce(t *testing.T) {
	t.Parallel()
	f := newObservabilityFixture(t)
	client := f.client(t, newTestClickHouse(t))
	ctx, cancel := context.WithTimeout(callerContext(t, workspaceReader), streamOpenLimit)
	defer cancel()
	start := time.Now()
	stream, err := client.TailLogs(ctx, &observabilityv1.TailLogsRequest{WorkspaceId: f.workspace.String()})
	if err != nil {
		t.Fatalf("TailLogs did not open within %v: %v", streamOpenLimit, err)
	}
	defer stream.Close()
	t.Logf("TailLogs opened after %v", time.Since(start))
}

func TestIdleStreamEventsOpensAtOnce(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	system := []genDb.EntityScope{{EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin}}
	rpcServer := connect.NewServer(scopeInjector(func() []genDb.EntityScope { return system }).Interceptor)
	eventv1connect.RegisterEventServiceHandler(rpcServer, f.events)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	client := eventv1connect.NewEventServiceClient(
		connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL)),
	)
	ctx, cancel := context.WithTimeout(t.Context(), streamOpenLimit)
	defer cancel()
	start := time.Now()
	stream, err := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
	if err != nil {
		t.Fatalf("StreamEvents did not open within %v: %v", streamOpenLimit, err)
	}
	defer stream.Close()
	t.Logf("StreamEvents opened after %v", time.Since(start))
}
