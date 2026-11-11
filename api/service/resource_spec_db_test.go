package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
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
	ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
	req := &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: f.resourceID.String()},
	}
	_, err := server.GetResource(ctx, req)
	wantServerFailure(t, err)
}

func TestCreateResourceRejectsNonServiceType(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
	service := &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{}}
	spec := &resourcev1.ResourceSpec{Spec: service}
	req := &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        "db",
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_DATABASE,
		Spec:        spec,
	}
	_, err := server.CreateResource(ctx, req)
	wantCode(t, err, connect.CodeInvalidArgument)
}

func TestCreateResourceEnforcesLogRetentionMaximum(t *testing.T) {
	f := newDeployFixture(t)
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
	defaults := testServiceDefaults()
	defaults.LogRetention = "2d"
	server := NewResourceServer(f.pool, f.queries, defaults, testClusterSilentAfter)
	for _, period := range []string{"3d", "-1h", "0s", "1.5s", "bad", "1d"} {
		spec := &resourcev1.ResourceSpec{
			Spec: &resourcev1.ResourceSpec_Service{
				Service: &resourcev1.ServiceSpec{
					Observability: &resourcev1.ObservabilityConfig{
						Logging: &resourcev1.LoggingConfig{RetentionPeriod: period},
					},
				},
			},
		}
		req := &resourcev1.CreateResourceRequest{
			WorkspaceId: workspaceID.String(),
			Name:        "retention-fixture",
			Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
			Spec:        spec,
		}
		_, err := server.CreateResource(ctx, req)
		if period == "1d" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		wantCode(t, err, connect.CodeInvalidArgument)
	}
}
