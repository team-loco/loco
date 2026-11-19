package service

import (
	"context"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const twoRegionsBiggerCPU = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
      eu-west-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`

// rolloutIDs reads the rollout ID of each deployment the response started.
func (f *deployFixture) rolloutIDs(t *testing.T, started []*planv1.StartedDeployment) []string {
	t.Helper()
	listed := f.listDeployments(t, &deploymentv1.ListDeploymentsRequest{
		WorkspaceId: f.workspaceID(t).String(), EnvironmentId: f.envID.String(),
	}, f.adminScopes(t)...)
	byID := make(map[string]string, len(listed.GetDeployments()))
	for _, deployment := range listed.GetDeployments() {
		byID[deployment.GetId()] = deployment.GetRolloutId()
	}
	ids := make([]string, 0, len(started))
	for _, deployment := range started {
		id, found := byID[deployment.GetDeploymentId()]
		if !found || id == "" {
			t.Fatalf("deployment %s lists rollout ID %q", deployment.GetDeploymentId(), id)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f *deployFixture) getDeployment(t *testing.T, id string) *deploymentv1.Deployment {
	t.Helper()
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: f.adminScopes(t)})
	resp, err := f.deploymentServer().GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: id})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return resp.GetDeployment()
}

func TestApplyAndScaleShareARolloutAcrossRegions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	addCluster := `
INSERT INTO clusters (platforms, name, region, provider, is_active, is_default, health_status, last_heartbeat)
VALUES (ARRAY['linux/amd64'], 'c3', $1, 'kind', true, false, 'healthy', NOW())`
	if _, err := f.pool.Exec(context.Background(), addCluster, otherRegion); err != nil {
		t.Fatalf("add cluster: %v", err)
	}

	first := f.rolloutIDs(t, applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments())
	if len(first) != 2 || first[0] != first[1] {
		t.Fatalf("rollout IDs = %v, want one shared by both regions", first)
	}

	f.finishRollouts(t)
	second := f.rolloutIDs(t, applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{}).GetDeployments())
	if len(second) != 2 || second[0] != second[1] || second[0] == first[0] {
		t.Fatalf("rollout IDs = %v after %v, want a new one shared by both regions", second, first)
	}

	if got := f.getDeployment(t, f.workerDeployments(t)[0].ID.String()).GetRolloutId(); got != second[0] {
		t.Fatalf("GetDeployment rollout ID = %q, want %q", got, second[0])
	}

	f.finishRollouts(t)
	worker, _ := f.resourceByName(t, applyWorker)
	replicas := int32(2)
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb, Scopes: f.adminScopes(t),
	})
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	if _, err := server.ScaleResource(ctx, &resourcev1.ScaleResourceRequest{
		ResourceId: worker.ID.String(), EnvironmentId: f.envID.String(), Replicas: &replicas,
	}); err != nil {
		t.Fatalf("scale: %v", err)
	}
	active := f.workerDeployments(t)
	scaled := make(map[string]bool, len(active))
	for _, deployment := range active {
		scaled[f.getDeployment(t, deployment.ID.String()).GetRolloutId()] = true
	}
	if len(active) != 2 || len(scaled) != 1 || scaled[second[0]] || scaled[""] {
		t.Fatalf("scaled rollout IDs = %v, want one new ID shared by both regions", scaled)
	}
}
