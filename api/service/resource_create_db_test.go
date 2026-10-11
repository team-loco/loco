package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestCreateResourceWithoutADomain(t *testing.T) {
	testCreateResourceDomain(t, false)
}

func TestCreateResourceWithAPlatformDomain(t *testing.T) {
	testCreateResourceDomain(t, true)
}

func testCreateResourceDomain(t *testing.T, platform bool) {
	t.Helper()
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
	var input *domainv1.DomainInput
	if platform {
		platformID, err := f.queries.CreatePlatformDomain(ctx, genDb.CreatePlatformDomainParams{
			Domain: "example.com", IsActive: true,
		})
		if err != nil {
			t.Fatalf("create platform domain: %v", err)
		}
		platformIDString := platformID.String()
		label := "private"
		input = &domainv1.DomainInput{
			DomainSource:     domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
			PlatformDomainId: &platformIDString, Subdomain: &label,
		}
	}
	req := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspace,
		Name:        "private",
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        spec,
		Domain:      input,
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
	domains := resource.GetDomains()
	if platform {
		if len(domains) != 1 || domains[0].GetDomain() != "private.example.com" {
			t.Fatalf("domains = %v, want private.example.com", domains)
		}
	} else if len(domains) != 0 {
		t.Fatalf("domains = %v, want none", domains)
	}
}

func TestCreateResourceRejectsCustomDomains(t *testing.T) {
	f := newDeployFixture(t)
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: resource.WorkspaceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	custom := "app.custom.example"
	service := &resourcev1.ServiceSpec{Routing: &resourcev1.RoutingConfig{Port: 8080}}
	spec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: service}}
	input := &domainv1.DomainInput{DomainSource: domainv1.DomainType_DOMAIN_TYPE_USER_PROVIDED, Domain: &custom}
	workspaceID := resource.WorkspaceID.String()
	request := connect.NewRequest(&resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID, Name: "custom", Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: spec, Domain: input,
	})
	_, err = server.CreateResource(ctx, request)
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("create error = %v, want Unimplemented", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM resources WHERE name = 'custom'`).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("custom resources = %d, error = %v, want none", count, err)
	}
}
