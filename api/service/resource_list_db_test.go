package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
)

const (
	listAlpha   = "Alpha"
	listBeta    = "beta"
	listGamma   = "gamma"
	listDelta   = "delta"
	listEpsilon = "epsilon"
	listSvc     = "svc"
	listImage   = "ghcr.io/acme/alpha:1.2"
	listCPU     = "500m"
	listMemory  = "512Mi"
)

// resourceListFixture is a workspace of six resources:
//
//	svc      prod us-ca-1 1/1 HEALTHY, running 4 CPU 256Mi, no configured region
//	Alpha    prod us-va-1 1/2 DEGRADED, staging us-ca-1 1/1 HEALTHY, image listImage, configured 1 CPU 512Mi
//	beta     staging us-ca-1 0/3 DEPLOYING, domain beta.example.test, configured 500m 1Gi
//	gamma    never deployed, slug custom-slug, configured 2 CPU 1Gi
//	delta    never deployed, configured 500m 512Mi
//	epsilon  prod deployment canceled, nothing live, configured 500m 512Mi
//
// Every resource but svc configures one region: us-va-1 for Alpha, us-ca-1 for the others.
type resourceListFixture struct {
	*resourceStatusFixture
	ids map[string]uuid.UUID
}

func newResourceListFixture(t *testing.T) *resourceListFixture {
	t.Helper()
	f := &resourceListFixture{resourceStatusFixture: newResourceStatusFixture(t), ids: map[string]uuid.UUID{}}
	f.ids[listSvc] = f.resourceID
	configured := map[string][3]string{
		listAlpha:   {statusSecondRegion, "1", listMemory},
		listBeta:    {testRegion, listCPU, "1Gi"},
		listGamma:   {testRegion, "2", "1Gi"},
		listDelta:   {testRegion, listCPU, listMemory},
		listEpsilon: {testRegion, listCPU, listMemory},
	}
	for _, name := range []string{listAlpha, listBeta, listGamma, listDelta, listEpsilon} {
		c := configured[name]
		f.ids[name] = f.tenant.resource(t, f.workspaceID, name, &resourcev1.ServiceSpec{
			Regions: map[string]*resourcev1.RegionTarget{c[0]: {Enabled: true, Cpu: c[1], Memory: c[2]}},
		})
	}
	start := time.Now().Add(-10 * time.Hour).UTC().Truncate(time.Second)
	for i, name := range []string{listSvc, listAlpha, listBeta, listGamma, listDelta, listEpsilon} {
		created := start.Add(time.Duration(i) * time.Minute)
		f.exec(t, "UPDATE resources SET created_at = $2 WHERE id = $1", f.ids[name], created)
	}
	f.exec(t, "UPDATE resources SET slug = 'custom-slug' WHERE id = $1", f.ids[listGamma])

	svc := f.deployResource(t, listSvc, f.clusterID, f.envID, testRegion, 1)
	f.reportReplicas(t, f.ids[listSvc], f.envID, f.clusterID, servingReplica("svc-a", "node-1", start))
	f.exec(t, `UPDATE deployments SET started_at = $2, spec = jsonb_set(spec, ARRAY['regions', $3],
		  spec->'regions'->$3 || '{"cpu":"4","memory":"256Mi"}') WHERE id = $1`, svc, start.Add(time.Hour), testRegion)

	alphaProd := f.deployResource(t, listAlpha, f.regionCluster, f.envID, statusSecondRegion, statusTwoReplicas)
	f.reportReplicas(t, f.ids[listAlpha], f.envID, f.regionCluster, servingReplica("alpha-a", "node-1", start))
	alphaStaging := f.deployResource(t, listAlpha, f.stagingCluster, f.stagingID, testRegion, 1)
	f.reportReplicas(t, f.ids[listAlpha], f.stagingID, f.stagingCluster, servingReplica("alpha-s", "node-2", start))
	f.exec(t, "UPDATE deployments SET started_at = $2 WHERE id = $1", alphaProd, start.Add(2*time.Hour))
	f.exec(t, `UPDATE deployments SET started_at = $2, spec = spec || '{"build":{"image":"`+listImage+`"}}'
		WHERE id = $1`, alphaStaging, start.Add(3*time.Hour))

	beta := f.deployResource(t, listBeta, f.stagingCluster, f.stagingID, testRegion, 3)
	f.exec(t, "UPDATE deployments SET started_at = $2 WHERE id = $1", beta, start.Add(4*time.Hour))
	f.exec(t, `INSERT INTO resource_domains (resource_id, environment_id, domain, domain_source, is_primary)
		VALUES ($1, $2, 'beta.example.test', 'user_provided', true)`, f.ids[listBeta], f.stagingID)

	epsilon := f.deployResource(t, listEpsilon, f.clusterID, f.envID, testRegion, 1)
	f.exec(t, "UPDATE deployment_placements SET status = 'canceled' WHERE deployment_id = $1", epsilon)
	f.exec(t, "UPDATE placements SET desired_deleted = true WHERE deployment_id = $1", epsilon)
	f.exec(t, "UPDATE deployments SET started_at = $2 WHERE id = $1", epsilon, start.Add(5*time.Hour))
	return f
}

func (f *resourceListFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *resourceListFixture) deployResource(
	t *testing.T,
	name string,
	clusterID, environmentID uuid.UUID,
	region string,
	replicas int32,
) uuid.UUID {
	t.Helper()
	params := f.paramsFor(clusterID)
	params.ResourceID, params.EnvironmentID, params.Region, params.Replicas = f.ids[name], environmentID, region, replicas
	var deploymentID uuid.UUID
	err := withTx(f.ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		deploymentID, deployErr = recordTestDeployment(
			f.ctx,
			qtx,
			limits.Install(limits.Defaults()),
			params,
			staticSpec,
		)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy %s to %s: %v", name, region, err)
	}
	return deploymentID
}

func (f *resourceListFixture) list(
	t *testing.T,
	req *resourcev1.ListWorkspaceResourcesRequest,
) *resourcev1.ListWorkspaceResourcesResponse {
	t.Helper()
	req = proto.CloneOf(req)
	req.WorkspaceId = f.workspaceID.String()
	response, err := f.server().ListWorkspaceResources(f.ctx, req)
	if err != nil {
		t.Fatalf("list resources %v: %v", req, err)
	}
	return response
}

// names lists every page of req, pageSize resources at a time.
func (f *resourceListFixture) names(
	t *testing.T,
	req *resourcev1.ListWorkspaceResourcesRequest,
	pageSize int32,
) []string {
	t.Helper()
	req = proto.CloneOf(req)
	req.PageSize = pageSize
	var names []string
	for {
		page := f.list(t, req)
		for _, resource := range page.GetResources() {
			names = append(names, resource.GetName())
		}
		if page.GetNextPageToken() == "" {
			return names
		}
		req.PageToken = page.GetNextPageToken()
	}
}

func TestListWorkspaceResourcesOrders(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	prod, staging := f.envID.String(), f.stagingID.String()
	type order = resourcev1.ResourceOrder
	cases := []struct {
		order       order
		environment string
		asc, desc   []string
	}{
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_UNSPECIFIED, "",
			[]string{listEpsilon, listDelta, listGamma, listBeta, listAlpha, listSvc},
			[]string{listEpsilon, listDelta, listGamma, listBeta, listAlpha, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_NAME, "",
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
			[]string{listSvc, listGamma, listEpsilon, listDelta, listBeta, listAlpha},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS, "",
			[]string{listAlpha, listBeta, listSvc, listDelta, listEpsilon, listGamma},
			[]string{listDelta, listEpsilon, listGamma, listSvc, listBeta, listAlpha},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS, prod,
			[]string{listAlpha, listSvc, listBeta, listDelta, listEpsilon, listGamma},
			[]string{listBeta, listDelta, listEpsilon, listGamma, listSvc, listAlpha},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_CREATED, "",
			[]string{listSvc, listAlpha, listBeta, listGamma, listDelta, listEpsilon},
			[]string{listEpsilon, listDelta, listGamma, listBeta, listAlpha, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_SIZE, "",
			[]string{listSvc, listDelta, listEpsilon, listBeta, listAlpha, listGamma},
			[]string{listGamma, listAlpha, listBeta, listDelta, listEpsilon, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_SIZE, prod,
			[]string{listDelta, listEpsilon, listBeta, listAlpha, listGamma, listSvc},
			[]string{listSvc, listGamma, listAlpha, listBeta, listDelta, listEpsilon},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_REGION, "",
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_REGION, prod,
			[]string{listBeta, listDelta, listEpsilon, listGamma, listSvc, listAlpha},
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_REGION, staging,
			[]string{listSvc, listAlpha, listBeta, listDelta, listEpsilon, listGamma},
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_REPLICAS, "",
			[]string{listDelta, listEpsilon, listGamma, listSvc, listAlpha, listBeta},
			[]string{listAlpha, listBeta, listSvc, listDelta, listEpsilon, listGamma},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_REPLICAS, prod,
			[]string{listBeta, listDelta, listEpsilon, listGamma, listSvc, listAlpha},
			[]string{listAlpha, listSvc, listBeta, listDelta, listEpsilon, listGamma},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_DEPLOYED, "",
			[]string{listDelta, listGamma, listSvc, listAlpha, listBeta, listEpsilon},
			[]string{listEpsilon, listBeta, listAlpha, listSvc, listDelta, listGamma},
		},
		{
			resourcev1.ResourceOrder_RESOURCE_ORDER_DEPLOYED, prod,
			[]string{listBeta, listDelta, listGamma, listSvc, listAlpha, listEpsilon},
			[]string{listEpsilon, listAlpha, listSvc, listBeta, listDelta, listGamma},
		},
	}
	for _, c := range cases {
		for _, descending := range []bool{false, true} {
			want := c.asc
			if descending {
				want = c.desc
			}
			req := &resourcev1.ListWorkspaceResourcesRequest{
				OrderBy: c.order, Descending: descending, EnvironmentId: c.environment,
			}
			for _, pageSize := range []int32{0, 1, 2} {
				if got := f.names(t, req, pageSize); !slices.Equal(got, want) {
					t.Errorf("%s descending=%t environment=%q page size %d = %v, want %v",
						c.order, descending, c.environment, pageSize, got, want)
				}
			}
		}
	}
}

func TestListWorkspaceResourcesFilters(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	prod, staging := f.envID.String(), f.stagingID.String()
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	cases := []struct {
		name string
		req  *resourcev1.ListWorkspaceResourcesRequest
		want []string
	}{
		{"name, any case", &resourcev1.ListWorkspaceResourcesRequest{Query: "ALPH"}, []string{listAlpha}},
		{"slug", &resourcev1.ListWorkspaceResourcesRequest{Query: "custom-sl"}, []string{listGamma}},
		{"domain", &resourcev1.ListWorkspaceResourcesRequest{Query: "beta.example"}, []string{listBeta}},
		{"image search", &resourcev1.ListWorkspaceResourcesRequest{Query: "acme/alpha:1"}, []string{listAlpha}},
		{"image in another environment", &resourcev1.ListWorkspaceResourcesRequest{
			Query: "acme/alpha", EnvironmentId: prod,
		}, nil},
		{"region", &resourcev1.ListWorkspaceResourcesRequest{Query: "us-va"}, []string{listAlpha}},
		{"live region in another environment", &resourcev1.ListWorkspaceResourcesRequest{
			Query: testRegion, EnvironmentId: staging,
		}, []string{listAlpha, listBeta, listDelta, listEpsilon, listGamma}},
		{"configured region", &resourcev1.ListWorkspaceResourcesRequest{
			Query: "us-va", EnvironmentId: staging,
		}, []string{listAlpha}},
		{"wildcards match literally", &resourcev1.ListWorkspaceResourcesRequest{Query: "%"}, nil},
		{"status in any environment", &resourcev1.ListWorkspaceResourcesRequest{Status: healthy},
			[]string{listAlpha, listSvc}},
		{"status in the environment", &resourcev1.ListWorkspaceResourcesRequest{Status: healthy, EnvironmentId: prod},
			[]string{listSvc}},
		{"degraded", &resourcev1.ListWorkspaceResourcesRequest{
			Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		}, []string{listAlpha}},
		{"not deployed anywhere", &resourcev1.ListWorkspaceResourcesRequest{NotDeployed: true},
			[]string{listDelta, listEpsilon, listGamma}},
		{"not deployed in the environment", &resourcev1.ListWorkspaceResourcesRequest{
			NotDeployed: true, EnvironmentId: prod,
		}, []string{listBeta, listDelta, listEpsilon, listGamma}},
		{
			"region filter",
			&resourcev1.ListWorkspaceResourcesRequest{Region: testRegion},
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc},
		},
		{"region filter in the environment", &resourcev1.ListWorkspaceResourcesRequest{
			Region: testRegion, EnvironmentId: prod,
		}, []string{listBeta, listDelta, listEpsilon, listGamma, listSvc}},
		{"region filter without a live deployment there", &resourcev1.ListWorkspaceResourcesRequest{
			Region: statusSecondRegion, EnvironmentId: staging,
		}, []string{listAlpha}},
		{"type", &resourcev1.ListWorkspaceResourcesRequest{Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE},
			[]string{listAlpha, listBeta, listDelta, listEpsilon, listGamma, listSvc}},
		{
			"another type",
			&resourcev1.ListWorkspaceResourcesRequest{Type: resourcev1.ResourceType_RESOURCE_TYPE_DATABASE},
			nil,
		},
		{"environment scoped", &resourcev1.ListWorkspaceResourcesRequest{EnvironmentScoped: true, EnvironmentId: prod},
			[]string{listAlpha, listDelta, listEpsilon, listGamma, listSvc}},
		{"environment scoped to staging", &resourcev1.ListWorkspaceResourcesRequest{
			EnvironmentScoped: true, EnvironmentId: staging,
		}, []string{listAlpha, listBeta, listDelta, listGamma}},
	}
	for _, c := range cases {
		c.req.OrderBy = resourcev1.ResourceOrder_RESOURCE_ORDER_NAME
		if got := f.names(t, c.req, 0); !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestListWorkspaceResourcesCounts(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	page := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{
		Status: healthy, Region: testRegion, PageSize: 1, IncludeTotalCount: true, IncludeFacets: true,
	})
	if page.GetTotalCount() != 2 {
		t.Errorf("total_count = %d, want 2", page.GetTotalCount())
	}
	want := &resourcev1.ResourceFacets{
		// Ignoring the status filter: every resource has us-ca-1, and gamma, delta and epsilon run nothing.
		Statuses: []*resourcev1.ResourceStatusCount{
			{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, Count: 1},
			{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING, Count: 1},
			{Status: healthy, Count: 2},
		},
		NotDeployed: 3,
		// Ignoring the region and status filters: every resource.
		Regions: []*resourcev1.ResourceRegionCount{
			{Region: testRegion, Count: 6},
			{Region: statusSecondRegion, Count: 1},
		},
		Types: []*resourcev1.ResourceTypeCount{{Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE, Count: 2}},
	}
	if !proto.Equal(page.GetFacets(), want) {
		t.Errorf("facets = %v, want %v", page.GetFacets(), want)
	}

	searched := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{Query: listBeta, IncludeFacets: true})
	wantRegions := []*resourcev1.ResourceRegionCount{
		{Region: testRegion, Count: 1},
		{Region: statusSecondRegion},
	}
	if got := searched.GetFacets().
		GetRegions(); !slices.EqualFunc(
		got,
		wantRegions,
		func(a, b *resourcev1.ResourceRegionCount) bool {
			return proto.Equal(a, b)
		},
	) {
		t.Errorf("regions for %q = %v, want %v", listBeta, got, wantRegions)
	}

	unfiltered := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{
		NotDeployed: true, EnvironmentId: f.envID.String(), IncludeFacets: true,
	})
	if got := unfiltered.GetFacets().GetNotDeployed(); got != 4 {
		t.Errorf("not deployed in prod = %d, want 4", got)
	}
	if got := unfiltered.GetTotalCount(); got != 0 {
		t.Errorf("total_count without include_total_count = %d", got)
	}
	if got := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{}).GetFacets(); got != nil {
		t.Errorf("facets without include_facets = %v", got)
	}
}

func TestListWorkspaceResourcesRejectsATokenOfAnotherOrder(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	first := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{
		PageSize: 1, OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS,
	})
	for _, req := range []*resourcev1.ListWorkspaceResourcesRequest{
		{OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_SIZE},
		{OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS, Descending: true},
		{},
	} {
		req.WorkspaceId, req.PageSize, req.PageToken = f.workspaceID.String(), 1, first.GetNextPageToken()
		_, err := f.server().ListWorkspaceResources(f.ctx, req)
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%v with a status token: %v, want InvalidArgument", req, err)
		}
	}
}

func TestListWorkspaceResourcesNeedsTheWorkspace(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	otherWorkspace := f.tenant.workspace(t, "other")
	otherResource := f.tenant.resource(t, otherWorkspace, "elsewhere", &resourcev1.ServiceSpec{})
	for name, scopes := range map[string][]genDb.EntityScope{
		"no grant": nil,
		"a resource of another workspace": {
			{EntityType: genDb.EntityTypeResource, EntityID: otherResource, Scope: genDb.ScopeRead},
		},
		"write and admin on a resource": {
			{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeWrite},
			{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeAdmin},
		},
	} {
		caller := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
		_, err := f.server().ListWorkspaceResources(caller, &resourcev1.ListWorkspaceResourcesRequest{
			WorkspaceId: f.workspaceID.String(),
		})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Errorf("caller with %s listed the workspace: %v, want PermissionDenied", name, err)
		}
	}

	_, err := f.server().ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(), EnvironmentId: f.tenant.defaultEnvironment(t, otherWorkspace).String(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Errorf("environment of another workspace: %v, want NotFound", err)
	}
}

func TestListWorkspaceResourcesListsTheGrantedResources(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	f.tenant.resource(t, f.workspaceID, "hidden", &resourcev1.ServiceSpec{
		Regions: map[string]*resourcev1.RegionTarget{"hidden-region": {Enabled: true}},
	})
	f.ctx = reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listAlpha], Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: f.ids[listBeta], Scope: genDb.ScopeRead},
	}})
	for _, pageSize := range []int32{0, 1} {
		got := f.names(t, &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS,
		}, pageSize)
		if want := []string{listAlpha, listBeta}; !slices.Equal(got, want) {
			t.Errorf("granted resources, page size %d = %v, want %v", pageSize, got, want)
		}
	}
	if got := f.names(t, &resourcev1.ListWorkspaceResourcesRequest{Region: testRegion}, 0); !slices.Equal(
		got, []string{listBeta, listAlpha}) {
		t.Errorf("granted resources in %s = %v, want beta, Alpha", testRegion, got)
	}

	page := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{
		PageSize: 1, IncludeTotalCount: true, IncludeFacets: true,
	})
	if page.GetTotalCount() != 2 {
		t.Errorf("total_count = %d, want 2", page.GetTotalCount())
	}
	want := &resourcev1.ResourceFacets{
		Statuses: []*resourcev1.ResourceStatusCount{
			{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, Count: 1},
			{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING, Count: 1},
			{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, Count: 1},
		},
		Regions: []*resourcev1.ResourceRegionCount{
			{Region: testRegion, Count: 2},
			{Region: statusSecondRegion, Count: 1},
		},
		Types: []*resourcev1.ResourceTypeCount{{Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE, Count: 2}},
	}
	if !proto.Equal(page.GetFacets(), want) {
		t.Errorf("facets = %v, want %v", page.GetFacets(), want)
	}
}

func TestListWorkspaceResourcesValidatesFilters(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	server := connect.NewServer(validate.NewServerInterceptor())
	resourcev1connect.RegisterResourceServiceHandler(server, f.server())
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	client := resourcev1connect.NewResourceServiceClient(
		connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL)))
	for _, req := range []*resourcev1.ListWorkspaceResourcesRequest{
		{Status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, NotDeployed: true},
		{EnvironmentScoped: true},
	} {
		req.WorkspaceId = f.workspaceID.String()
		if _, err := client.ListWorkspaceResources(
			t.Context(),
			req,
		); connect.CodeOf(
			err,
		) != connect.CodeInvalidArgument {
			t.Errorf("%v: %v, want InvalidArgument", req, err)
		}
	}
}

func TestListWorkspaceResourcesSortsByAnotherEnvironment(t *testing.T) {
	t.Parallel()
	f := newResourceListFixture(t)
	prod, staging := f.envID.String(), f.stagingID.String()
	status := resourcev1.ResourceOrder_RESOURCE_ORDER_STATUS
	cases := []struct {
		name string
		req  *resourcev1.ListWorkspaceResourcesRequest
		want []string
	}{
		{"every environment's regions, prod's status", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: status, SortEnvironmentId: prod, Region: statusSecondRegion,
		}, []string{listAlpha}},
		{"every resource, prod's status", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: status, SortEnvironmentId: prod,
		}, []string{listAlpha, listSvc, listBeta, listDelta, listEpsilon, listGamma}},
		{"healthy anywhere, staging's status", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: status, SortEnvironmentId: staging,
			Status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		}, []string{listAlpha, listSvc}},
		{"staging's status, descending", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: status, SortEnvironmentId: staging, Descending: true,
		}, []string{listDelta, listEpsilon, listGamma, listSvc, listAlpha, listBeta}},
		{"prod's resources, staging's status", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy:           status,
			SortEnvironmentId: staging,
			EnvironmentId:     prod,
			Status:            resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		}, []string{listSvc}},
		{"prod's size across environments", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_SIZE, SortEnvironmentId: prod,
		}, []string{listDelta, listEpsilon, listBeta, listAlpha, listGamma, listSvc}},
		{"prod's deploy time across environments", &resourcev1.ListWorkspaceResourcesRequest{
			OrderBy: resourcev1.ResourceOrder_RESOURCE_ORDER_DEPLOYED, SortEnvironmentId: prod,
		}, []string{listBeta, listDelta, listGamma, listSvc, listAlpha, listEpsilon}},
	}
	for _, c := range cases {
		for _, pageSize := range []int32{0, 1, 2} {
			if got := f.names(t, c.req, pageSize); !slices.Equal(got, c.want) {
				t.Errorf("%s, page size %d = %v, want %v", c.name, pageSize, got, c.want)
			}
		}
	}

	first := f.list(t, &resourcev1.ListWorkspaceResourcesRequest{
		PageSize: 1, OrderBy: status, SortEnvironmentId: prod,
	})
	for _, sortEnvironment := range []string{staging, ""} {
		_, err := f.server().ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
			WorkspaceId: f.workspaceID.String(), PageSize: 1, OrderBy: status,
			SortEnvironmentId: sortEnvironment, PageToken: first.GetNextPageToken(),
		})
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("prod's status token sorting by %q: %v, want InvalidArgument", sortEnvironment, err)
		}
	}

	otherWorkspace := f.tenant.workspace(t, "other")
	_, err := f.server().ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(), OrderBy: status,
		SortEnvironmentId: f.tenant.defaultEnvironment(t, otherWorkspace).String(),
	})
	if codeOf(err) != connect.CodeNotFound {
		t.Errorf("sorting by another workspace's environment: %v, want NotFound", err)
	}
}
