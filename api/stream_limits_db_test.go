package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/tvm"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/internal/bearer"
)

// heldEvents sends one batch on each event stream and holds it open until the client leaves.
type heldEvents struct {
	eventv1connect.UnimplementedEventServiceHandler
}

func (heldEvents) ListEvents(context.Context, *eventv1.ListEventsRequest) (*eventv1.ListEventsResponse, error) {
	return &eventv1.ListEventsResponse{}, nil
}

func (heldEvents) StreamEvents(
	ctx context.Context,
	_ *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) error {
	if err := stream.Send(&eventv1.StreamEventsResponse{}); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func TestAuthenticatedServerCapsStreamsButNotUnaryCalls(t *testing.T) {
	t.Parallel()
	queries := genDb.New(dbtest.NewPool(t))
	user, err := queries.CreateUser(t.Context(), genDb.CreateUserParams{Email: "streams@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	machine := tvm.NewVendingMachine(queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	token, _, err := machine.IssueSession(t.Context(), user.ID, netip.Addr{}, "streams")
	if err != nil {
		t.Fatal(err)
	}
	config := &APIConfig{
		RPCTimeout:   time.Minute,
		StreamAuth:   auth.StreamConfig{Interval: time.Minute, Timeout: time.Second},
		StreamLimits: interceptor.StreamLimits{PerReplica: 1, PerPrincipal: 1},
	}
	base, authenticated, err := newRPCServers(
		t.Context(), config, &deps{machine: machine, authorizer: authz.New(queries)}, apiMeter(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	eventv1connect.RegisterEventServiceHandler(authenticated, heldEvents{})
	mux := http.NewServeMux()
	mountRPCServers(mux, base, authenticated)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	client := eventv1connect.NewEventServiceClient(
		connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL)),
	)
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", bearer.Header(token))
	open := func() error {
		stream, openErr := client.StreamEvents(ctx, &eventv1.StreamEventsRequest{})
		if openErr != nil {
			return openErr
		}
		t.Cleanup(func() {
			if closeErr := stream.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		_, openErr = stream.Receive()
		return openErr
	}
	if err = open(); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if err = open(); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("stream past the cap = %v, want resource_exhausted", err)
	}
	if _, err = client.ListEvents(ctx, &eventv1.ListEventsRequest{OrgId: uuid.NewString()}); err != nil {
		t.Fatalf("unary call at the stream cap: %v", err)
	}
}
