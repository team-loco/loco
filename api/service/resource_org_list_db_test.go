package service

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// orgResourceNames lists every page of req for the caller's scopes, pageSize resources at a time.
func (f *resourceListFixture) orgResourceNames(
	t *testing.T,
	scopes []genDb.EntityScope,
	req *resourcev1.ListOrgResourcesRequest,
	pageSize int32,
) []string {
	t.Helper()
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	req.OrgId, req.PageSize = f.tenant.orgID.String(), pageSize
	var names []string
	for {
		page, err := f.server().ListOrgResources(ctx, req)
		if err != nil {
			t.Fatalf("list organization resources %v: %v", req, err)
		}
		for _, r := range page.GetResources() {
			names = append(names, r.GetName()+"@"+r.GetWorkspaceName())
		}
		if page.GetNextPageToken() == "" {
			return names
		}
		req.PageToken = page.GetNextPageToken()
	}
}

func TestListOrgResourcesListsWhatTheCallerReads(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	other := f.tenant.workspace(t, "other")
	f.tenant.resource(t, other, "remote", &resourcev1.ServiceSpec{
		Regions: map[string]*resourcev1.RegionTarget{testRegion: {Enabled: true}},
	})
	var home string
	if err := f.pool.QueryRow(context.Background(), "SELECT name FROM workspaces WHERE id = $1", f.workspaceID).
		Scan(&home); err != nil {
		t.Fatal(err)
	}
	in := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = n + "@" + home
		}
		return out
	}
	workspaceReader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeMember},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeRead},
	}
	newest := in(listEpsilon, listDelta, listGamma, listBeta, listAlpha, listSvc)
	for _, pageSize := range []int32{0, 2} {
		got := f.orgResourceNames(t, workspaceReader, &resourcev1.ListOrgResourcesRequest{}, pageSize)
		if !slices.Equal(got, newest) {
			t.Errorf("workspace reader, page size %d = %v, want %v", pageSize, got, newest)
		}
	}
	bySlug := f.orgResourceNames(t, workspaceReader, &resourcev1.ListOrgResourcesRequest{Query: "CUSTOM"}, 0)
	if got := bySlug; !slices.Equal(got, in(listGamma)) {
		t.Errorf("search by slug = %v, want gamma", got)
	}

	orgReader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeRead},
	}
	if got := f.orgResourceNames(t, orgReader, &resourcev1.ListOrgResourcesRequest{Query: "mot"}, 0); !slices.Equal(
		got, []string{"remote@other"}) {
		t.Errorf("organization reader searching mot = %v, want remote in other", got)
	}

	granted := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeMember},
		{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeRead},
	}
	if got := f.orgResourceNames(t, granted, &resourcev1.ListOrgResourcesRequest{}, 0); !slices.Equal(
		got, in(listAlpha)) {
		t.Errorf("resource grantee = %v, want Alpha only", got)
	}

	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: workspaceReader})
	page, err := f.server().ListOrgResources(ctx, &resourcev1.ListOrgResourcesRequest{
		OrgId: f.tenant.orgID.String(), PageSize: 1, IncludeTotalCount: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.GetTotalCount() != int32(len(newest)) || page.GetNextPageToken() == "" {
		t.Errorf(
			"first page total %d token %q, want %d and a token",
			page.GetTotalCount(),
			page.GetNextPageToken(),
			len(newest),
		)
	}

	outsider := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: uuid.New(), Scope: genDb.ScopeRead},
	}
	ctx = reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: outsider})
	if _, err = f.server().ListOrgResources(ctx, &resourcev1.ListOrgResourcesRequest{
		OrgId: f.tenant.orgID.String(),
	}); err == nil {
		t.Error("a caller outside the organization listed its resources")
	}
}

func TestListOrgResourcesKeepsToTheCallersGrantsInTheOrganization(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	foreign := seedTenant(t, f.pool, f.queries, "foreign@org-resources.test", "foreign")
	foreignWorkspace := foreign.workspace(t, "abroad")
	foreignResource := foreign.resource(t, foreignWorkspace, "abroad", &resourcev1.ServiceSpec{})
	gone := f.tenant.resource(t, f.workspaceID, "gone", &resourcev1.ServiceSpec{})
	if _, err := f.pool.Exec(context.Background(), "DELETE FROM resources WHERE id = $1", gone); err != nil {
		t.Fatal(err)
	}
	member := genDb.EntityScope{
		EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeMember,
	}
	cases := map[string][]genDb.EntityScope{
		"member without grants": {member},
		"grants in another organization": {
			member,
			{EntityType: genDb.EntityTypeOrganization, EntityID: foreign.orgID, Scope: genDb.ScopeRead},
			{EntityType: genDb.EntityTypeWorkspace, EntityID: foreignWorkspace, Scope: genDb.ScopeRead},
			{EntityType: genDb.EntityTypeResource, EntityID: foreignResource, Scope: genDb.ScopeRead},
		},
		"resource grant without read": {
			member,
			{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeWrite},
		},
		"grant on a deleted resource": {
			member,
			{EntityType: genDb.EntityTypeResource, EntityID: gone, Scope: genDb.ScopeRead},
		},
	}
	for name, scopes := range cases {
		if got := f.orgResourceNames(t, scopes, &resourcev1.ListOrgResourcesRequest{}, 0); len(got) != 0 {
			t.Errorf("%s = %v, want none", name, got)
		}
	}

	orgReader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeOrganization, EntityID: f.tenant.orgID, Scope: genDb.ScopeRead},
	}
	for _, query := range []string{"%", "_"} {
		req := &resourcev1.ListOrgResourcesRequest{Query: query}
		if got := f.orgResourceNames(t, orgReader, req, 0); len(got) != 0 {
			t.Errorf("query %q = %v, want none", query, got)
		}
	}
}
