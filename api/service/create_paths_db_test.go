package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const dashboardService = "dashboard"

// The dashboard creates a service with CreateResource and CreateDeployment, loco.yaml with
// PlanService.Apply; the cluster must run the same service either way.
func TestDashboardAndLocoYAMLCreateTheSameService(t *testing.T) {
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
	service := &deploymentv1.ServiceDeploymentSpec{
		Build:       &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: resolved.GetImage()},
		Cpu:         &cpu,
		Memory:      &memory,
		MinReplicas: &replicas,
		MaxReplicas: &replicas,
		Port:        &port,
	}
	if _, err = deployments.CreateDeployment(ctx, &deploymentv1.CreateDeploymentRequest{
		ResourceId:    created.GetResourceId(),
		Region:        testRegion,
		EnvironmentId: f.envID.String(),
		Spec:          &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	}); err != nil {
		t.Fatal(err)
	}
	fromDashboard := f.servicePayload(t, uuid.MustParse(created.GetResourceId())).AppSpec.ServiceSpec

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

func (f *deployFixture) servicePayload(t *testing.T, resourceID uuid.UUID) locoControllerV1.PlacementPayload {
	t.Helper()
	key := genDb.GetPlacementForResourceClusterParams{ResourceID: resourceID, ClusterID: f.clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(context.Background(), key)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	var payload locoControllerV1.PlacementPayload
	if err = json.Unmarshal(placement.DesiredSpec, &payload); err != nil {
		t.Fatalf("decode desired spec: %v", err)
	}
	return payload
}
