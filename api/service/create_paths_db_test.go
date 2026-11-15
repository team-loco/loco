package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const dashboardService = "dashboard"

// The dashboard creates a service with CreateResource and CreateDeployment, loco.yaml with
// PlanService.Apply; the cluster must run the same service either way.
func TestDashboardAndLocoYAMLCreateTheSameService(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+`  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`, applyOptions{})
	fromFile := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec

	resources := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	deployments := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: testDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
	)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser,
		Method:    genDb.DeploymentMethodWeb,
		Scopes:    f.applyScopes(t),
	})
	workspaceID := f.workspaceID(t).String()
	resolved, err := deployments.ResolveImage(
		ctx, &deploymentv1.ResolveImageRequest{WorkspaceId: workspaceID, Image: testPublicImage},
	)
	if err != nil {
		t.Fatal(err)
	}
	port, cpu, memory, replicas := int32(8080), testRegionCPU, testRegionMemory, int32(1)
	created, err := resources.CreateResource(ctx, &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID,
		Name:        dashboardService,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
			Routing: &resourcev1.RoutingConfig{Port: &port},
			Regions: map[string]*resourcev1.RegionTarget{testRegion: {
				Enabled: true, Cpu: cpu, Memory: memory, MinReplicas: replicas, MaxReplicas: replicas,
			}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.resourceID = uuid.MustParse(created.GetResourceId())
	if _, err = deployService(t, f, &deploymentv1.ServiceDeploymentSpec{
		Build:       &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: resolved.GetImage()},
		Cpu:         &cpu,
		Memory:      &memory,
		MinReplicas: &replicas,
		MaxReplicas: &replicas,
		Port:        &port,
	}); err != nil {
		t.Fatal(err)
	}
	fromDashboard := desiredPayload(t, f).AppSpec.ServiceSpec

	file, err := json.Marshal(fromFile)
	if err != nil {
		t.Fatal(err)
	}
	dashboard, err := json.Marshal(fromDashboard)
	if err != nil {
		t.Fatal(err)
	}
	if string(file) != string(dashboard) {
		t.Fatalf("service spec from loco.yaml:\n%s\nfrom the dashboard:\n%s", file, dashboard)
	}
}
