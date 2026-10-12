package rpc_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	firstBearerToken  = "first"
	secondBearerToken = "second"
)

type authServer struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*authServer) GetConfig(ctx context.Context, _ *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	return &configv1.GetConfigResponse{SchemaUrl: info.RequestHeader().Get("Authorization")}, nil
}
func TestBearerCredentialsAreBoundToClient(t *testing.T) {
	mux := http.NewServeMux()
	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(server, &authServer{})
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for _, token := range []string{firstBearerToken, secondBearerToken, ""} {
		t.Run(token, func(t *testing.T) {
			clients := rpc.NewHTTPClients(srv.Client(), srv.URL, token)
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					response, err := clients.Config.GetConfig(t.Context(), &configv1.GetConfigRequest{})
					if err != nil {
						t.Error(err)
						return
					}
					want := ""
					if token != "" {
						want = "Bearer " + token
					}
					if got := response.GetSchemaUrl(); got != want {
						t.Errorf("Authorization = %q, want %q", got, want)
					}
				})
			}
			wg.Wait()
		})
	}
}
func TestForHostSharesConnectionsAcrossCredentials(t *testing.T) {
	mux := http.NewServeMux()
	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(server, &authServer{})
	connecthttp.Mount(mux, server)
	srv := httptest.NewUnstartedServer(mux)
	var connections atomic.Int32
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	for _, token := range []string{firstBearerToken, secondBearerToken, ""} {
		clients := rpc.ForHost(srv.URL, token)
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
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("opened %d connections, want 1 shared connection", got)
	}
}
