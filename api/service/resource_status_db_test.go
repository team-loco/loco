package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	secondRegion   = "eu-west-1"
	failureMessage = "container app exited with code 1: listen tcp :8080: bind: address already in use"
)

type resourceStatusFixture struct {
	*deployFixture
	workspaceID   uuid.UUID
	secondCluster uuid.UUID
	ctx           context.Context
}

func newResourceStatusFixture(t *testing.T) *resourceStatusFixture {
	t.Helper()
	f := &resourceStatusFixture{deployFixture: newDeployFixture(t)}
	ctx := context.Background()
	row := f.pool.QueryRow(ctx, `
WITH c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c3', $2, 'kind', true, true) RETURNING id
), rr AS (
    INSERT INTO resource_regions (resource_id, region, is_primary, status)
    VALUES ($1, $2, false, 'active') RETURNING id
)
SELECT c.id, r.workspace_id FROM c, rr, resources r WHERE r.id = $1`, f.resourceID, secondRegion)
	if err := row.Scan(&f.secondCluster, &f.workspaceID); err != nil {
		t.Fatalf("seed second region: %v", err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeRead},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeAdmin},
	}
	f.ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	return f
}

func (f *resourceStatusFixture) deployRegion(t *testing.T, clusterID uuid.UUID, region string) uuid.UUID {
	t.Helper()
	params := f.paramsFor(clusterID)
	params.Region = region
	var id uuid.UUID
	err := withTx(f.ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = createDeploymentWithCleanup(f.ctx, qtx, params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy %s: %v", region, err)
	}
	return id
}

func (f *resourceStatusFixture) report(t *testing.T, clusterID uuid.UUID, status *agentv1.PlacementStatus) {
	t.Helper()
	p := f.placement(t, clusterID)
	status.PlacementId = p.ID.String()
	status.ObservedRevision = p.DesiredRevision
	f.agentServer().recordStatus(f.ctx, clusterID, status)
}

func (f *resourceStatusFixture) ready(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	f.report(t, clusterID, &agentv1.PlacementStatus{Ready: true, ReadyReplicas: 1})
}

func (f *resourceStatusFixture) fail(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	f.report(t, clusterID, &agentv1.PlacementStatus{Phase: applicationPhaseFailed, Message: failureMessage})
}

func (f *resourceStatusFixture) assertStatus(t *testing.T, want resourcev1.ResourceStatus) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	resourceID := f.resourceID.String()

	got, err := server.GetResource(f.ctx, connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	}))
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if status := got.Msg.GetResource().GetStatus(); status != want {
		t.Errorf("GetResource status = %s, want %s", status, want)
	}

	statusResp, err := server.GetResourceStatus(f.ctx, connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: resourceID,
	}))
	if err != nil {
		t.Fatalf("get resource status: %v", err)
	}
	if status := statusResp.Msg.GetResource().GetStatus(); status != want {
		t.Errorf("GetResourceStatus status = %s, want %s", status, want)
	}

	listed, err := server.ListWorkspaceResources(f.ctx, connect.NewRequest(&resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	}))
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	resources := listed.Msg.GetResources()
	if len(resources) != 1 {
		t.Fatalf("listed %d resources, want 1", len(resources))
	}
	if status := resources[0].GetStatus(); status != want {
		t.Errorf("ListWorkspaceResources status = %s, want %s", status, want)
	}
}

func TestResourceStatusWithoutDeploymentIsUnspecified(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED)
}

func TestResourceStatusIsDeployingUntilEveryRegionIsReady(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING)

	f.ready(t, f.clusterID)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING)
}

func TestResourceStatusIsHealthyWhenEveryRegionIsRunning(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.ready(t, f.clusterID)
	f.ready(t, f.secondCluster)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
}

func TestResourceStatusIsDegradedWhenOneRegionFails(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.ready(t, f.clusterID)
	f.fail(t, f.secondCluster)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED)
}

func TestResourceStatusIsUnavailableWhenEveryRegionFails(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.fail(t, f.clusterID)
	f.fail(t, f.secondCluster)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE)
}

func TestResourceStatusIgnoresDeletedDeployments(t *testing.T) {
	f := newResourceStatusFixture(t)
	running := f.deployRegion(t, f.clusterID, testRegion)
	failed := f.deployRegion(t, f.secondCluster, secondRegion)
	f.ready(t, f.clusterID)
	f.fail(t, f.secondCluster)

	server := NewDeploymentServer(f.pool, f.queries, nil, testRegistryHost, testServiceDefaults())
	deleteDeployment := func(id uuid.UUID) {
		t.Helper()
		req := connect.NewRequest(&deploymentv1.DeleteDeploymentRequest{DeploymentId: id.String()})
		if _, err := server.DeleteDeployment(f.ctx, req); err != nil {
			t.Fatalf("delete deployment: %v", err)
		}
	}

	deleteDeployment(failed)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)

	deleteDeployment(running)
	f.assertStatus(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED)
}

func TestDeletedResourceHasNoStatus(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.ready(t, f.clusterID)

	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	resourceID := f.resourceID.String()
	deleteReq := connect.NewRequest(&resourcev1.DeleteResourceRequest{ResourceId: resourceID})
	if _, err := server.DeleteResource(f.ctx, deleteReq); err != nil {
		t.Fatalf("delete resource: %v", err)
	}

	_, err := server.GetResourceStatus(f.ctx, connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: resourceID,
	}))
	if err == nil {
		t.Fatal("GetResourceStatus succeeded for a deleted resource")
	}

	listed, err := server.ListWorkspaceResources(f.ctx, connect.NewRequest(&resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	}))
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if n := len(listed.Msg.GetResources()); n != 0 {
		t.Fatalf("listed %d resources after delete, want 0", n)
	}
}

func (f *resourceStatusFixture) assertRegionErrors(t *testing.T, want map[string]string) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	resourceID := f.resourceID.String()

	got, err := server.GetResource(f.ctx, connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	}))
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	gotRegions := got.Msg.GetResource().GetRegions()
	assertRegionErrorsIn(t, "GetResource", gotRegions, want)

	statusResp, err := server.GetResourceStatus(f.ctx, connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: resourceID,
	}))
	if err != nil {
		t.Fatalf("get resource status: %v", err)
	}
	statusRegions := statusResp.Msg.GetResource().GetRegions()
	assertRegionErrorsIn(t, "GetResourceStatus", statusRegions, want)

	listed, err := server.ListWorkspaceResources(f.ctx, connect.NewRequest(&resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	}))
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	resources := listed.Msg.GetResources()
	if len(resources) != 1 {
		t.Fatalf("listed %d resources, want 1", len(resources))
	}
	listedRegions := resources[0].GetRegions()
	assertRegionErrorsIn(t, "ListWorkspaceResources", listedRegions, want)
}

func assertRegionErrorsIn(t *testing.T, rpc string, regions []*resourcev1.RegionConfig, want map[string]string) {
	t.Helper()
	if len(regions) != len(want) {
		t.Fatalf("%s returned %d regions, want %d", rpc, len(regions), len(want))
	}
	for _, region := range regions {
		wantErr, ok := want[region.GetRegion()]
		if !ok {
			t.Fatalf("%s returned unexpected region %q", rpc, region.GetRegion())
		}
		if got := region.GetLastError(); got != wantErr {
			t.Errorf("%s region %s last_error = %q, want %q", rpc, region.GetRegion(), got, wantErr)
		}
	}
}

func TestFailedRegionCarriesDeploymentFailureMessage(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.ready(t, f.clusterID)
	f.fail(t, f.secondCluster)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: failureMessage})
}

func TestHealthyAndDeployingRegionsHaveNoLastError(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: ""})

	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: ""})

	f.ready(t, f.clusterID)
	f.ready(t, f.secondCluster)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: ""})
}

func TestRecoveredRegionClearsLastError(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployRegion(t, f.clusterID, testRegion)
	f.deployRegion(t, f.secondCluster, secondRegion)
	f.ready(t, f.clusterID)
	f.fail(t, f.secondCluster)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: failureMessage})

	f.ready(t, f.secondCluster)
	f.assertRegionErrors(t, map[string]string{testRegion: "", secondRegion: ""})
}
