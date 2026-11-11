package main

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db/dbtest"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/admission"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/service"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

func TestAdmissionExemptsControlPlane(t *testing.T) {
	pool := dbtest.ClosedPool(t)
	queries := db.New(pool)
	config := admission.Config{Window: time.Minute, GlobalRequests: 1}
	signer, err := ingesttest.NewSigner("test")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	limiter := admission.New(pool, backend, config)
	d := &deps{
		admission: limiter,
		auth:      service.NewAuthServer(pool, queries, nil, backend, auth.Admins{}, "https://dashboard.test"),
		config:    service.NewConfigServer("", "", servicedefaults.Defaults{}, nil, nil),
		agent:     service.NewAgentServer(pool, queries, nil, nil, nil, time.Second, service.AgentConfig{}),
		ingest: service.NewIngestServer(
			queries,
			service.NewClusterAuthenticator(queries, time.Minute),
			admissionSigner{signer},
			time.Minute,
		),
	}
	base, authenticated, err := newRPCServers(t.Context(), &APIConfig{RPCTimeout: time.Second}, d)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerHandlers(mux, base, authenticated, d)
	mountRPCServers(mux, base, authenticated)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL))
	if _, firstErr := authv1connect.NewAuthServiceClient(client).
		StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{}); firstErr != nil {
		t.Fatal(firstErr)
	}
	_, err = authv1connect.NewAuthServiceClient(client).StartDeviceLogin(t.Context(), &authv1.StartDeviceLoginRequest{})
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("request admitted past its budget: %v", err)
	}
	_, err = configv1connect.NewConfigServiceClient(client).GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	_, err = ingestv1connect.NewIngestServiceClient(client).IssueToken(t.Context(), &ingestv1.IssueTokenRequest{})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("ingest did not reach its authentication: %v", err)
	}
	stream, err := agentv1connect.NewAgentServiceClient(client).Sync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if sendErr := stream.Send(&agentv1.SyncRequest{}); sendErr != nil {
		t.Fatal(sendErr)
	}
	_, err = stream.Receive()
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("sync did not reach its authentication: %v", err)
	}
	for _, path := range []string{healthPath, ingesttoken.JWKSPath} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s", path, response.Status)
		}
	}
}

type admissionSigner struct{ ingesttest.Signer }

func (s admissionSigner) PublicKeys(context.Context) (map[string]ed25519.PublicKey, error) {
	return map[string]ed25519.PublicKey{s.ID: ed25519.PublicKey(s.Key[ed25519.SeedSize:])}, nil
}
