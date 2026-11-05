package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestGetResourceFailsOnCorruptSpec(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	corrupt := `UPDATE resources SET spec = '{"regions": 7}' WHERE id = $1`
	if _, err := f.pool.Exec(ctx, corrupt, f.resourceID); err != nil {
		t.Fatalf("corrupt spec: %v", err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: f.resourceID.String()},
	}
	_, err := server.GetResource(ctx, req)
	wantCode(t, err, connect.CodeInternal)
}
