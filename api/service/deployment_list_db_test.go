package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
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
) *deploymentv1.ListDeploymentsResponse {
	t.Helper()
	server := NewDeploymentServer(f.pool, f.queries, nil, "", testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	scoped := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
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
