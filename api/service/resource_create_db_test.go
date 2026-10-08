package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestCreateResourceWithoutADomain(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	scoped := context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)

	region := &resourcev1.RegionTarget{Enabled: true, Primary: true, Cpu: testRegionCPU, Memory: testRegionMemory}
	service := &resourcev1.ServiceSpec{
		Routing: &resourcev1.RoutingConfig{Port: 8080},
		Regions: map[string]*resourcev1.RegionTarget{testRegion: region},
	}
	spec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: service}}
	workspace := workspaceID.String()
	req := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspace,
		Name:        "private",
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        spec,
	})
	created, err := server.CreateResource(scoped, req)
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}

	resource, err := getResourceByName(t, f, scopes, workspaceID, "private")
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if resource.GetId() != created.Msg.GetResourceId() {
		t.Fatalf("resource = %s, want %s", resource.GetId(), created.Msg.GetResourceId())
	}
	if domains := resource.GetDomains(); len(domains) != 0 {
		t.Fatalf("domains = %v, want none", domains)
	}
}
