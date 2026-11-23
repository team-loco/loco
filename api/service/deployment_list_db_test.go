package service

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func (f *deployFixture) deployStaging(t *testing.T, stagingID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	staging := f.paramsFor(f.otherCluster)
	staging.EnvironmentID = stagingID
	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = recordTestDeployment(ctx, qtx, limits.Install(limits.Defaults()), staging, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy staging: %v", err)
	}
	return id
}

func (f *deployFixture) listDeployments(
	t *testing.T,
	req *deploymentv1.ListDeploymentsRequest,
	grants ...genDb.EntityScope,
) *deploymentv1.ListDeploymentsResponse {
	t.Helper()
	server := f.deploymentServer()
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	if grants != nil {
		scopes = grants
	}
	scoped := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	resp, err := server.ListDeployments(scoped, req)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	return resp
}

func (f *deployFixture) deploymentServer() *DeploymentServer {
	return NewDeploymentServer(
		f.pool,
		f.queries,
		nil,
		"",
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
		8,
		nil,
	)
}

func deploymentIDs(deployments []*deploymentv1.Deployment) []string {
	ids := make([]string, 0, len(deployments))
	for _, d := range deployments {
		ids = append(ids, d.GetId())
	}
	return ids
}

func TestListDeploymentsFiltersByEnvironmentAcrossPages(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)

	ctx := context.Background()

	first, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	stagingDeployment := f.deployStaging(t, stagingID)
	second, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("redeploy production: %v", err)
	}

	all := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String()})
	if got := len(all.GetDeployments()); got != 3 {
		t.Fatalf("unfiltered deployments = %v, want all 3", deploymentIDs(all.GetDeployments()))
	}

	staging := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: stagingID.String(),
	})
	if got := deploymentIDs(staging.GetDeployments()); len(got) != 1 || got[0] != stagingDeployment.String() {
		t.Fatalf("staging deployments = %v, want only %s", got, stagingDeployment)
	}

	production := &deploymentv1.ListDeploymentsRequest{
		ResourceId:        f.resourceID.String(),
		EnvironmentId:     f.envID.String(),
		PageSize:          1,
		IncludeTotalCount: true,
	}
	page := f.listDeployments(t, production)
	if got := deploymentIDs(page.GetDeployments()); len(got) != 1 || got[0] != second.String() {
		t.Fatalf("first production page = %v, want %s", got, second)
	}
	if page.GetTotalCount() != 2 {
		t.Errorf("production total = %d, want 2", page.GetTotalCount())
	}
	previous := func(d *deploymentv1.Deployment) []string {
		ids := make([]string, 0, len(d.GetPlacements()))
		for _, placement := range d.GetPlacements() {
			ids = append(ids, placement.GetPreviousDeploymentId())
		}
		return ids
	}
	if got := previous(page.GetDeployments()[0]); !slices.Equal(got, []string{first.String()}) {
		t.Errorf("previous of %s = %v, want %s, not the staging deployment between them", second, got, first)
	}
	production.PageToken = page.GetNextPageToken()
	page = f.listDeployments(t, production)
	if got := deploymentIDs(page.GetDeployments()); len(got) != 1 || got[0] != first.String() {
		t.Fatalf("second production page = %v, want %s, not the staging deployment between them", got, first)
	}
	if got := previous(page.GetDeployments()[0]); !slices.Equal(got, []string{""}) {
		t.Errorf("previous of the first deployment = %v, want none", got)
	}
}

func TestListDeploymentsPreviousIsInTheSameRegion(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	regionCluster := f.addOtherRegionCluster(t)

	first, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy %s: %v", testRegion, err)
	}
	var elsewhere uuid.UUID
	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		params := f.paramsFor(regionCluster)
		params.Region = otherRegion
		var deployErr error
		elsewhere, deployErr = recordTestDeployment(ctx, qtx, limits.Install(limits.Defaults()), params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy %s: %v", otherRegion, err)
	}
	second, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("redeploy %s: %v", testRegion, err)
	}

	page := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{ResourceId: f.resourceID.String()})
	previous := map[string]string{}
	for _, d := range page.GetDeployments() {
		for _, placement := range d.GetPlacements() {
			previous[d.GetId()] = placement.GetPreviousDeploymentId()
		}
	}
	want := map[string]string{second.String(): first.String(), elsewhere.String(): "", first.String(): ""}
	if !maps.Equal(previous, want) {
		t.Errorf("previous deployments = %v, want %v, skipping the deployment in %s", previous, want, otherRegion)
	}
}

func TestListDeploymentsWorkspaceEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	resource, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := resource.WorkspaceID
	stagingID := f.addStagingEnvironment(t)
	secondResource := f.tenant.resource(t, workspaceID, "second", &resourcev1.ServiceSpec{})
	hiddenResource := f.tenant.resource(t, workspaceID, "hidden", &resourcev1.ServiceSpec{})
	foreignResource := f.tenant.resource(t, f.tenant.workspace(t, "other"), "foreign", &resourcev1.ServiceSpec{})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var visible []string
	for i, seed := range []struct{ resource, env uuid.UUID }{
		{f.resourceID, f.envID},
		{secondResource, f.envID},
		{hiddenResource, f.envID},
		{f.resourceID, stagingID},
		{foreignResource, f.envID},
		{f.resourceID, f.envID},
	} {
		params := f.paramsFor(f.clusterID)
		params.ResourceID = seed.resource
		params.EnvironmentID = seed.env
		id, createErr := f.queries.CreateDeployment(ctx, params.CreateDeploymentParams)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, err = f.pool.Exec(
			ctx,
			"UPDATE deployments SET created_at = $2 WHERE id = $1",
			id,
			base.Add(time.Duration(i)*time.Hour),
		)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 1 || i == 5 {
			visible = append([]string{id.String()}, visible...)
		}
	}
	grants := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: secondResource, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeResource, EntityID: hiddenResource, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeResource, EntityID: foreignResource, Scope: genDb.ScopeRead},
	}
	request := &deploymentv1.ListDeploymentsRequest{
		WorkspaceId: workspaceID.String(), EnvironmentId: f.envID.String(), PageSize: 1,
	}
	var got []string
	for {
		page := f.listDeployments(t, request, grants...)
		got = append(got, deploymentIDs(page.GetDeployments())...)
		request.PageToken = page.GetNextPageToken()
		if request.GetPageToken() == "" {
			break
		}
	}
	if !slices.Equal(got, visible) {
		t.Fatalf("pages = %v, want %v", got, visible)
	}

	request.PageToken = ""
	request.PageSize = 200
	request.StartTime = timestamppb.New(base.Add(time.Hour))
	request.EndTime = timestamppb.New(base.Add(5 * time.Hour))
	page := f.listDeployments(t, request, grants...)
	if ids := deploymentIDs(page.GetDeployments()); !slices.Equal(ids, visible[1:2]) {
		t.Fatalf("window = %v, want %v", ids, visible[1:2])
	}
	request.StartTime, request.EndTime = nil, nil
	for _, scope := range []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeOrganization, EntityID: func() uuid.UUID {
			id, lookupErr := f.queries.GetOrganizationIDByWorkspaceID(ctx, workspaceID)
			if lookupErr != nil {
				t.Fatal(lookupErr)
			}
			return id
		}(), Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeRead},
	} {
		page = f.listDeployments(t, request, scope)
		if len(page.GetDeployments()) != 4 {
			t.Fatalf("%s scope: got %v", scope.EntityType, deploymentIDs(page.GetDeployments()))
		}
	}
	page = f.listDeployments(
		t,
		request,
		genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: uuid.New(), Scope: genDb.ScopeRead},
	)
	if len(page.GetDeployments()) != 0 {
		t.Fatal("unrelated workspace grant exposes deployments")
	}
	resourcePage := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{ResourceId: hiddenResource.String()},
		genDb.EntityScope{EntityType: genDb.EntityTypeResource, EntityID: hiddenResource, Scope: genDb.ScopeWrite})
	if len(resourcePage.GetDeployments()) != 0 {
		t.Fatal("unreadable resource exposes deployments")
	}

	request.WorkspaceId = uuid.NewString()
	page = f.listDeployments(t, request, grants...)
	if len(page.GetDeployments()) != 0 {
		t.Fatal("workspace filter exposes deployments")
	}
}

func TestListDeploymentsScopeAndWindowValidation(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	for _, request := range []*deploymentv1.ListDeploymentsRequest{
		{},
		{WorkspaceId: id},
		{ResourceId: id, WorkspaceId: id, EnvironmentId: id},
		{ResourceId: "bad-resource-id"},
		{ResourceId: id, StartTime: timestamppb.Now(), EndTime: timestamppb.New(time.Time{})},
	} {
		if err := protovalidate.Validate(request); err == nil {
			t.Fatalf("accepted invalid request: %v", request)
		}
	}
	for _, request := range []*deploymentv1.ListDeploymentsRequest{
		{ResourceId: id},
		{ResourceId: id, EnvironmentId: id},
		{WorkspaceId: id, EnvironmentId: id},
		{WorkspaceId: id, EnvironmentId: id, StartTime: timestamppb.New(time.Time{}), EndTime: timestamppb.Now()},
	} {
		if err := protovalidate.Validate(request); err != nil {
			t.Fatal(err)
		}
	}
}
