package service

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const twoRegionsBiggerCPU = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
      eu-west-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`

func (f *deployFixture) getDeployment(t *testing.T, id string) *deploymentv1.Deployment {
	t.Helper()
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: f.adminScopes(t)})
	resp, err := f.deploymentServer().GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: id})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return resp.GetDeployment()
}

func (f *deployFixture) addOtherRegionCluster(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	addCluster := `
INSERT INTO clusters (platforms, name, region, provider, is_active, is_default, health_status, last_heartbeat)
VALUES (ARRAY['linux/amd64'], 'c3', $1, 'kind', true, false, 'healthy', NOW()) RETURNING id`
	if err := f.pool.QueryRow(context.Background(), addCluster, otherRegion).Scan(&id); err != nil {
		t.Fatalf("add cluster: %v", err)
	}
	return id
}

// placementRegions lists the regions of a deployment and their statuses.
func placementRegions(deployment *deploymentv1.Deployment) map[string]deploymentv1.PlacementStatus {
	regions := map[string]deploymentv1.PlacementStatus{}
	for _, placement := range deployment.GetPlacements() {
		regions[placement.GetRegion()] = placement.GetStatus()
	}
	return regions
}

func (f *deployFixture) scaleWorker(t *testing.T, req *resourcev1.ScaleResourceRequest) {
	t.Helper()
	worker, _ := f.resourceByName(t, applyWorker)
	req.ResourceId, req.EnvironmentId = worker.ID.String(), f.envID.String()
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb, Scopes: f.adminScopes(t),
	})
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	if _, err := server.ScaleResource(ctx, req); err != nil {
		t.Fatalf("scale: %v", err)
	}
}

func TestApplyAndScaleCreateOneDeploymentAcrossRegions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.addOtherRegionCluster(t)

	started := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()
	both := []string{otherRegion, testRegion}
	if len(started) != 1 || !slices.Equal(started[0].GetRegions(), both) {
		t.Fatalf("started = %v, want one deployment in %v", started, both)
	}
	first := f.getDeployment(t, started[0].GetDeploymentId())
	if len(first.GetPlacements()) != 2 ||
		first.GetTrigger() != deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_DEPLOY {
		t.Fatalf("deployment = %v, want a deploy with a placement per region", first)
	}
	live := f.workerDeployments(t)
	if len(live) != 2 || live[0].ID != live[1].ID || live[0].ID.String() != first.GetId() {
		t.Fatalf("live = %+v, want both regions running %s", live, first.GetId())
	}

	f.finishRollouts(t)
	second := applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{}).GetDeployments()
	if len(second) != 1 || second[0].GetDeploymentId() == first.GetId() {
		t.Fatalf("started = %v, want one new deployment", second)
	}
	f.finishRollouts(t)
	if got := f.getDeployment(t, first.GetId()).GetStatus(); got != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED {
		t.Fatalf("first deployment = %s, want superseded once the second runs everywhere", got)
	}

	f.scaleWorker(t, &resourcev1.ScaleResourceRequest{Replicas: new(int32(2))})
	live = f.workerDeployments(t)
	if len(live) != 2 || live[0].ID != live[1].ID || live[0].ID.String() == second[0].GetDeploymentId() {
		t.Fatalf("live after scale = %+v, want one new deployment in both regions", live)
	}
	scaled := f.getDeployment(t, live[0].ID.String())
	if scaled.GetTrigger() != deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_SCALE ||
		scaled.GetSourceDeploymentId() != second[0].GetDeploymentId() {
		t.Fatalf("scaled = %v, want a scale copying %s", scaled, second[0].GetDeploymentId())
	}
	for _, placement := range scaled.GetPlacements() {
		if placement.GetDesiredReplicas() != 2 {
			t.Fatalf("placement %s replicas = %d, want 2", placement.GetRegion(), placement.GetDesiredReplicas())
		}
	}
}

func TestRegionScopedScaleLeavesTheOtherRegionLive(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.addOtherRegionCluster(t)
	started := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()
	f.finishRollouts(t)
	original := started[0].GetDeploymentId()

	f.scaleWorker(t, &resourcev1.ScaleResourceRequest{Replicas: new(int32(3)), Region: new(otherRegion)})
	byRegion := map[string]string{}
	for _, region := range f.workerDeployments(t) {
		byRegion[region.Region] = region.ID.String()
	}
	if byRegion[testRegion] != original || byRegion[otherRegion] == original {
		t.Fatalf("live = %v, want %s only scaled away from %s", byRegion, otherRegion, original)
	}
	scaled := f.getDeployment(t, byRegion[otherRegion])
	if regions := placementRegions(scaled); len(regions) != 1 {
		t.Fatalf("scaled placements = %v, want only %s", regions, otherRegion)
	}

	f.finishRollouts(t)
	got := placementRegions(f.getDeployment(t, original))
	if got[testRegion] != deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING ||
		got[otherRegion] != deploymentv1.PlacementStatus_PLACEMENT_STATUS_SUPERSEDED {
		t.Fatalf("original placements = %v, want %s running and %s superseded", got, testRegion, otherRegion)
	}
	if status := f.getDeployment(t, original).GetStatus(); status != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
		t.Fatalf("original = %s, want running while one region serves it", status)
	}

	f.scaleWorker(t, &resourcev1.ScaleResourceRequest{Cpu: new(biggerCPU)})
	live := f.workerDeployments(t)
	if len(live) != 2 || live[0].ID == live[1].ID {
		t.Fatalf("live = %+v, want each region's deployment copied on its own", live)
	}
}

func TestANewDeploymentCancelsAnUnstartedOne(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.addOtherRegionCluster(t)
	first := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()[0]
	second := applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{}).GetDeployments()[0]

	canceled := f.getDeployment(t, first.GetDeploymentId())
	if canceled.GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED {
		t.Fatalf("first = %s, want canceled by the newer deployment", canceled.GetStatus())
	}
	var supersededBy []uuid.UUID
	rows, err := f.pool.Query(t.Context(), `SELECT superseded_by FROM deployment_placements WHERE deployment_id = $1`,
		first.GetDeploymentId())
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		supersededBy = append(supersededBy, id)
	}
	rows.Close()
	want := uuid.MustParse(second.GetDeploymentId())
	if len(supersededBy) != 2 || supersededBy[0] != want || supersededBy[1] != want {
		t.Fatalf("superseded by = %v, want %s in both regions", supersededBy, want)
	}
}
