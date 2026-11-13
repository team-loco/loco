package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestCreateResourceWithoutADomain(t *testing.T) {
	testCreateResourceDomain(t, false, false)
}

func TestCreateResourceWithAPlatformDomain(t *testing.T) {
	testCreateResourceDomain(t, true, false)
}

func TestCreateResourceWithAStagingPlatformDomain(t *testing.T) {
	testCreateResourceDomain(t, true, true)
}

func testCreateResourceDomain(t *testing.T, platform, staging bool) {
	t.Helper()
	f := newDeployFixture(t)
	ctx := context.Background()
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	scoped := reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})

	region := &resourcev1.RegionTarget{Enabled: true, Cpu: testRegionCPU, Memory: testRegionMemory}
	service := &resourcev1.ServiceSpec{
		Routing: &resourcev1.RoutingConfig{Port: 8080},
		Regions: map[string]*resourcev1.RegionTarget{testRegion: region},
	}
	spec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: service}}
	workspace := workspaceID.String()
	var input *domainv1.DomainInput
	var requestedEnvironment *string
	wantEnvironment := f.envID
	if platform {
		f.serveDomains(t, testDomainPlatform)
		if staging {
			wantEnvironment = f.addStagingEnvironment(t)
			requestedEnvironment = new(wantEnvironment.String())
			f.setStagingTier(t, f.clusterID)
			_, err := f.pool.Exec(ctx, `UPDATE clusters SET domains = '{}' WHERE id = $1`, f.otherCluster)
			if err != nil {
				t.Fatalf("clear production cluster domains: %v", err)
			}
		}
		label := "private"
		input = &domainv1.DomainInput{
			DomainSource:   domainv1.DomainType_DOMAIN_TYPE_PLATFORM_PROVIDED,
			PlatformDomain: new(testDomainPlatform), Subdomain: &label,
		}
	}
	req := &resourcev1.CreateResourceRequest{
		WorkspaceId:   workspace,
		Name:          "private",
		Type:          resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:          spec,
		Domain:        input,
		EnvironmentId: requestedEnvironment,
	}
	created, err := server.CreateResource(scoped, req)
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}

	resource, err := getResourceByName(t, f, scopes, workspaceID, "private")
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if resource.GetId() != created.GetResourceId() {
		t.Fatalf("resource = %s, want %s", resource.GetId(), created.GetResourceId())
	}
	domains := resource.GetDomains()
	if !platform {
		if len(domains) != 0 {
			t.Fatalf("domains = %v, want none", domains)
		}
		return
	}
	if len(domains) != 1 || domains[0].GetDomain() != "private.example.com" {
		t.Fatalf("domains = %v, want private.example.com", domains)
	}
	var environmentID uuid.UUID
	err = f.pool.QueryRow(ctx, `SELECT environment_id FROM resource_domains WHERE resource_id = $1`,
		resource.GetId()).Scan(&environmentID)
	if err != nil || environmentID != wantEnvironment {
		t.Fatalf("domain environment = %s, err = %v, want %s", environmentID, err, wantEnvironment)
	}
	req.Name = "other"
	_, err = server.CreateResource(scoped, req)
	wantCode(t, err, connect.CodeAlreadyExists)
	if _, err = getResourceByName(t, f, scopes, workspaceID, "other"); codeOf(err) != connect.CodeNotFound {
		t.Fatalf("resource with a taken domain: err = %v, want NotFound", err)
	}

	if staging {
		var otherEnvironment uuid.UUID
		err = f.pool.QueryRow(ctx, `
WITH w AS (
    INSERT INTO workspaces (org_id, name, created_by)
    SELECT org_id, 'other', created_by FROM workspaces WHERE id = $1 RETURNING id, created_by
)
INSERT INTO environments (workspace_id, name, created_by)
SELECT id, 'prod', created_by FROM w RETURNING id`, workspaceID).Scan(&otherEnvironment)
		if err != nil {
			t.Fatalf("create another workspace's environment: %v", err)
		}
		req.EnvironmentId = new(otherEnvironment.String())
		_, err = server.CreateResource(scoped, req)
		wantCode(t, err, connect.CodeNotFound)
	}
}
