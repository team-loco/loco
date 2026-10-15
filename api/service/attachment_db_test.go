package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/google/uuid"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

const attachmentRegion = "us-east-1"

func TestAttachmentCreatesAndReusesCluster(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "DELETE FROM clusters"); err != nil {
		t.Fatal(err)
	}
	server := f.agentServer()
	rpcServer := connect.NewServer()
	agentv1connect.RegisterAgentServiceHandler(rpcServer, server)
	transport := connectinprocess.New(rpcServer)
	client := agentv1connect.NewAgentServiceClient(connect.NewClient(transport))
	key := uuid.NewString()
	request := func(name, region, tier string) *agentv1.RegisterRequest {
		return &agentv1.RegisterRequest{
			ClusterKey:    key,
			DisplayName:   name,
			ClusterRegion: region,
			Tier:          tier,
			AgentVersion:  "test",
			Capacity:      &agentv1.AgentCapacity{CpuMillicoresTotal: 1000, MemoryBytesTotal: 1024},
		}
	}
	call := func(token string, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
		callCtx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer "+token)
		return client.Register(callCtx, req)
	}
	count := func(want int) {
		t.Helper()
		var got int
		if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM clusters").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("clusters = %d, want %d", got, want)
		}
	}
	if _, err := call(
		"wrong",
		request("first", attachmentRegion, environmentTypeProduction),
	); connect.CodeOf(
		err,
	) != connect.CodeUnauthenticated {
		t.Fatalf("invalid credential: %v", err)
	}
	count(0)
	if _, err := call(testAgentToken, &agentv1.RegisterRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid metadata: %v", err)
	}
	count(0)
	if _, err := call(
		testAgentToken,
		request(strings.Repeat("x", 256), attachmentRegion, environmentTypeProduction),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("oversized display name: %v", err)
	}
	count(0)
	first, err := call(testAgentToken, request("first", attachmentRegion, environmentTypeProduction))
	if err != nil {
		t.Fatal(err)
	}
	count(1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			resp, err := call(testAgentToken, request("renamed", attachmentRegion, environmentTypeProduction))
			if err != nil {
				t.Error(err)
				return
			}
			if resp.GetClusterId() != first.GetClusterId() {
				t.Errorf("reconnect changed identity: %s", resp.GetClusterId())
			}
		})
	}
	wg.Wait()
	count(1)
	for _, req := range []*agentv1.RegisterRequest{
		request("bad", "us-west-2", environmentTypeProduction),
		request("bad", attachmentRegion, "staging"),
	} {
		if _, err := call(testAgentToken, req); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("metadata drift: %v", err)
		}
	}
	var name string
	var healthy bool
	if err := f.pool.QueryRow(ctx, "SELECT name, COALESCE(health_status = 'healthy', false) FROM clusters").
		Scan(&name, &healthy); err != nil {
		t.Fatal(err)
	}
	if name != "renamed" || healthy {
		t.Fatalf("cluster name %q healthy %v", name, healthy)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE clusters SET is_active = false"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(testAgentToken, request("paused", attachmentRegion, environmentTypeProduction)); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err := f.pool.QueryRow(ctx, "SELECT is_active FROM clusters").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("registration resumed paused cluster")
	}
	if _, err := f.pool.Exec(ctx, "UPDATE clusters SET removed_at = NOW()"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(
		testAgentToken,
		request("removed", attachmentRegion, environmentTypeProduction),
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("removed cluster: %v", err)
	}
	count(1)
}

func TestConnectionSecretsOverlap(t *testing.T) {
	const replacementSecret = "new-secret"
	server := &AgentServer{config: AgentConfig{ConnectionSecrets: []string{"old-secret", replacementSecret}}}
	for _, token := range []string{"old-secret", replacementSecret} {
		if err := server.authenticateSecret("Bearer " + token); err != nil {
			t.Fatal(err)
		}
	}
	server.config.ConnectionSecrets = []string{replacementSecret}
	if err := server.authenticateSecret("Bearer old-secret"); err == nil {
		t.Fatal("retired secret accepted")
	}
	if err := server.authenticateSecret("Bearer new-secret"); err != nil {
		t.Fatal(err)
	}
}
