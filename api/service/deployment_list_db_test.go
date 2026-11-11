package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func (f *deployFixture) deployStaging(t *testing.T, stagingID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	staging := f.paramsFor(f.otherCluster)
	staging.EnvironmentID = stagingID
	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = createDeploymentWithCleanup(ctx, qtx, staging, staticSpec)
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
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		nil,
		"",
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
	)
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

func deploymentIDs(deployments []*deploymentv1.Deployment) []string {
	ids := make([]string, 0, len(deployments))
	for _, d := range deployments {
		ids = append(ids, d.GetId())
	}
	return ids
}

func TestListDeploymentsFiltersByEnvironmentAcrossPages(t *testing.T) {
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)
	f.setStagingTier(t, f.otherCluster)
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
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		PageSize:      1,
	}
	page := f.listDeployments(t, production)
	if got := deploymentIDs(page.GetDeployments()); len(got) != 1 || got[0] != second.String() {
		t.Fatalf("first production page = %v, want %s", got, second)
	}
	production.PageToken = page.GetNextPageToken()
	page = f.listDeployments(t, production)
	if got := deploymentIDs(page.GetDeployments()); len(got) != 1 || got[0] != first.String() {
		t.Fatalf("second production page = %v, want %s, not the staging deployment between them", got, first)
	}
}

func TestListDeploymentsWorkspaceEnvironment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	resource, err := f.queries.GetResourceByID(ctx, f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := resource.WorkspaceID
	stagingID := f.addStagingEnvironment(t)
	var secondResource, hiddenResource, foreignResource uuid.UUID
	for _, seed := range []struct {
		name    string
		id      *uuid.UUID
		foreign bool
	}{
		{"second", &secondResource, false},
		{"hidden", &hiddenResource, false},
		{"foreign", &foreignResource, true},
	} {
		ws := workspaceID
		if seed.foreign {
			err = f.pool.QueryRow(ctx, `INSERT INTO workspaces (org_id, name, created_by)
				SELECT org_id, 'other', created_by FROM workspaces WHERE id = $1 RETURNING id`, workspaceID).Scan(&ws)
			if err != nil {
				t.Fatal(err)
			}
		}
		err = f.pool.QueryRow(ctx, `INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
			VALUES ($1, $2, $2, 'service', '', '{}', 1) RETURNING id`, ws, seed.name).
			Scan(seed.id)
		if err != nil {
			t.Fatal(err)
		}
	}
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
		params.IsActive = false
		id, createErr := f.queries.CreateDeployment(ctx, params)
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
	request.Since = timestamppb.New(base.Add(time.Hour))
	request.Until = timestamppb.New(base.Add(5 * time.Hour))
	page := f.listDeployments(t, request, grants...)
	if ids := deploymentIDs(page.GetDeployments()); !slices.Equal(ids, visible[1:2]) {
		t.Fatalf("window = %v, want %v", ids, visible[1:2])
	}
	request.Since, request.Until = nil, nil
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
	request.WorkspaceId = uuid.NewString()
	page = f.listDeployments(t, request, grants...)
	if len(page.GetDeployments()) != 0 {
		t.Fatal("workspace filter exposes deployments")
	}
}

func TestListDeploymentsRejectsInvalidScopeAndWindow(t *testing.T) {
	f := newDeployFixture(t)
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		nil,
		"",
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
	)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{})
	for _, request := range []*deploymentv1.ListDeploymentsRequest{
		{},
		{WorkspaceId: uuid.NewString()},
		{ResourceId: f.resourceID.String(), WorkspaceId: uuid.NewString(), EnvironmentId: f.envID.String()},
		{ResourceId: "bad-resource-id"},
		{ResourceId: f.resourceID.String(), Since: timestamppb.Now(), Until: timestamppb.New(time.Time{})},
	} {
		_, err := server.ListDeployments(ctx, request)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%v: %v", request, err)
		}
	}
}
