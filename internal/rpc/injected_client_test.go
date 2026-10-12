package rpc_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	"github.com/team-loco/loco/gen/go/loco/event/v1/eventv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

type streamingAuthServer struct {
	eventv1connect.UnimplementedEventServiceHandler
}

func (*streamingAuthServer) StreamEvents(
	ctx context.Context,
	_ *eventv1.StreamEventsRequest,
	stream eventv1connect.EventServiceStreamEventsServerStream,
) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	response := &eventv1.StreamEventsResponse{Cursor: info.RequestHeader().Get("Authorization")}
	return stream.Send(response)
}

func TestInjectedConnectClientAuthenticatesUnaryAndStreamingCalls(t *testing.T) {
	transports := []struct {
		name   string
		create func(*testing.T, *connect.Server) connect.Transport
	}{
		{name: "HTTP", create: func(t *testing.T, server *connect.Server) connect.Transport {
			t.Helper()
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			httpServer := httptest.NewServer(mux)
			t.Cleanup(httpServer.Close)
			return connecthttp.NewTransport(httpServer.Client(), httpServer.URL)
		}},
		{name: "in-process", create: func(_ *testing.T, server *connect.Server) connect.Transport {
			return connectinprocess.New(server)
		}},
	}
	for _, transportCase := range transports {
		t.Run(transportCase.name, func(t *testing.T) {
			server := connect.NewServer()
			configv1connect.RegisterConfigServiceHandler(server, &authServer{})
			eventv1connect.RegisterEventServiceHandler(server, &streamingAuthServer{})
			transport := transportCase.create(t, server)
			for _, token := range []string{firstBearerToken, secondBearerToken, ""} {
				t.Run(token, func(t *testing.T) {
					interceptor := rpc.BearerInterceptor(token)
					connectClient := connect.NewClient(transport, interceptor)
					clients := rpc.NewClients(connectClient)
					configClient := clients.Config
					response, err := configClient.GetConfig(t.Context(), &configv1.GetConfigRequest{})
					if err != nil {
						t.Fatal(err)
					}
					want := ""
					if token != "" {
						want = "Bearer " + token
					}
					if got := response.GetSchemaUrl(); got != want {
						t.Fatalf("unary Authorization = %q, want %q", got, want)
					}
					eventClient := clients.Event
					stream, err := eventClient.StreamEvents(t.Context(), &eventv1.StreamEventsRequest{})
					if err != nil {
						t.Fatal(err)
					}
					defer stream.Close()
					batch, err := stream.Receive()
					if err != nil {
						t.Fatal(err)
					}
					if got := batch.GetCursor(); got != want {
						t.Fatalf("streaming Authorization = %q, want %q", got, want)
					}
					if _, err := stream.Receive(); !errors.Is(err, io.EOF) {
						t.Fatalf("stream ended with %v, want EOF", err)
					}
				})
			}
		})
	}
}
