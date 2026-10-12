package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

const oversizedMessageBytes = (4 << 20) + 1

type limitServer struct {
	agentv1connect.UnimplementedAgentServiceHandler
	called atomic.Bool
}

func (s *limitServer) Register(context.Context, *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	s.called.Store(true)
	return &agentv1.RegisterResponse{}, nil
}

func TestHTTPTransportKeepsDefaultMessageLimit(t *testing.T) {
	handler := &limitServer{}
	server := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(server, handler)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := agentv1connect.NewAgentServiceClient(connect.NewClient(transport))
	version := strings.Repeat("x", oversizedMessageBytes)
	_, err := client.Register(t.Context(), &agentv1.RegisterRequest{AgentVersion: version})
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("code = %s (%v), want resource_exhausted", code, err)
	}
	if handler.called.Load() {
		t.Fatal("oversized request reached handler")
	}
}
