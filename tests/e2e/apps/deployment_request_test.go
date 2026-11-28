package apps

import (
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestDeploymentRequestRetry(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "retry-deploy"
	resource, err := p.User.Resource.CreateResource(t.Context(), &resourcev1.CreateResourceRequest{
		WorkspaceId: w.ID, Name: app, Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
			Routing: &resourcev1.RoutingConfig{Port: new(int32(8080))},
			Regions: map[string]*resourcev1.RegionTarget{p.Region: {Enabled: true, MinReplicas: 1, MaxReplicas: 1}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &deploymentv1.CreateDeploymentRequest{
		RequestId:     uuid.NewString(),
		ResourceId:    resource.GetResourceId(),
		EnvironmentId: w.EnvironmentID,
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: imageBuild, Image: p.PublicImage}, Port: new(int32(8080)),
			}},
		},
	}
	first, err := p.User.Deployment.CreateDeployment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := p.User.Deployment.CreateDeployment(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(first, repeated) {
		t.Fatalf("same request returned another rollout: %v / %v", first, repeated)
	}
	waitServing(t, w, app, first.GetDeploymentId())
	if count := deploymentCount(t, resource.GetResourceId()); count != 1 {
		t.Fatalf("retry started %d deployments, want 1", count)
	}
}
