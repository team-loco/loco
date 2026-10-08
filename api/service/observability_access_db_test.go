package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const testProxyURL = "https://obs.us-east-1.loco.test"

func observabilityAccess(t *testing.T, f *deployFixture) []*observabilityv1.ClusterAccess {
	t.Helper()
	ctx := context.Background()
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}

	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewObservabilityAccessServer(f.pool, f.queries, machine)

	scope := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead}
	scopes := []genDb.EntityScope{scope}
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&observabilityv1.GetObservabilityAccessRequest{WorkspaceId: workspaceID.String()})
	resp, err := server.GetObservabilityAccess(ctx, req)
	if err != nil {
		t.Fatalf("get observability access: %v", err)
	}
	return resp.Msg.GetClusters()
}

func TestObservabilityAccessIncludesClustersThatRanBuilds(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(
		ctx,
		`UPDATE clusters SET observability_proxy_endpoint = $2 WHERE id = $1`,
		f.clusterID,
		testProxyURL,
	); err != nil {
		t.Fatalf("set proxy endpoint: %v", err)
	}

	if clusters := observabilityAccess(t, f); len(clusters) != 0 {
		t.Fatalf("clusters before any build or deployment = %v, want none", clusters)
	}

	if _, err := f.pool.Exec(ctx, `
INSERT INTO builds (resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, created_by)
SELECT $1, $2, 'running', 'upload', 'sources/x.tar.gz', 10, 'Dockerfile', 'registry.loco.test/x', id
FROM users LIMIT 1`, f.resourceID, f.clusterID); err != nil {
		t.Fatalf("insert build: %v", err)
	}

	clusters := observabilityAccess(t, f)
	if len(clusters) != 1 {
		t.Fatalf("clusters after a build = %v, want the build's cluster", clusters)
	}
	if got := clusters[0]; got.GetClusterId() != f.clusterID.String() || got.GetProxyUrl() != testProxyURL {
		t.Fatalf("cluster = %v, want %s at %s", got, f.clusterID, testProxyURL)
	}

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if clusters = observabilityAccess(t, f); len(clusters) != 1 {
		t.Fatalf("clusters after a build and a deployment on one cluster = %v, want one", clusters)
	}
}
