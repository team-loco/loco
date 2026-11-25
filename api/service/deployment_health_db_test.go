package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/proto"
)

const storedHealthPath = "/stored"

const checkedRegionSpec = `{"routing":{"port":8080},"healthCheck":{"path":"/stored","intervalSeconds":10,` +
	`"timeoutSeconds":2,"failureThreshold":3},"regions":{"us-ca-1":{"enabled":true,"cpu":"100m",` +
	`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`

func TestCreateDeploymentHealthCheck(t *testing.T) {
	t.Parallel()
	requested := &deploymentv1.HealthCheckConfig{
		Path:             "/new",
		IntervalSeconds:  20,
		TimeoutSeconds:   4,
		FailureThreshold: 2,
	}
	for name, tc := range map[string]struct {
		health       *deploymentv1.HealthCheckConfig
		remove       bool
		wantDeployed string
		wantResource string
	}{
		"omitted inherits the resource's": {wantDeployed: storedHealthPath, wantResource: storedHealthPath},
		"set runs the requested one":      {health: requested, wantDeployed: "/new", wantResource: storedHealthPath},
		"removed runs none and forgets":   {remove: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			seedDeployment(t, f, checkedRegionSpec)
			service := imageService()
			service.HealthCheck = tc.health
			spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}}
			_, err := deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
				ResourceId:        f.resourceID.String(),
				Regions:           []string{testRegion},
				EnvironmentId:     f.envID.String(),
				Spec:              spec,
				RemoveHealthCheck: tc.remove,
			})
			if err != nil {
				t.Fatalf("create deployment: %v", err)
			}
			if got := deployedHealthPath(t, f); got != tc.wantDeployed {
				t.Fatalf("deployed health check path = %q, want %q", got, tc.wantDeployed)
			}
			if got := f.resourceHealthCheck(t).GetPath(); got != tc.wantResource {
				t.Fatalf("resource health check path = %q, want %q", got, tc.wantResource)
			}

			f.finishRollouts(t)
			if _, err := deployService(t, f, imageService()); err != nil {
				t.Fatalf("deploy again: %v", err)
			}
			if got := deployedHealthPath(t, f); got != tc.wantResource {
				t.Fatalf("next deployment health check path = %q, want %q", got, tc.wantResource)
			}
		})
	}
}

func TestCreateDeploymentRejectsRemovingAHealthCheckItSets(t *testing.T) {
	t.Parallel()
	service := imageService()
	service.HealthCheck = workerHealthCheck()
	for name, tc := range map[string]struct {
		message proto.Message
		valid   bool
	}{
		"remove without a check": {removeRequest(imageService()), true},
		"remove with a check":    {removeRequest(service), false},
	} {
		t.Run(name, func(t *testing.T) {
			err := protovalidate.Validate(tc.message)
			if (err == nil) != tc.valid {
				t.Fatalf("validate = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func removeRequest(service *deploymentv1.ServiceDeploymentSpec) *deploymentv1.CreateDeploymentRequest {
	return &deploymentv1.CreateDeploymentRequest{
		ResourceId:        "01a1dbb5-4966-72e2-a92b-a2560006942a",
		EnvironmentId:     "01a1dbb5-4966-72e2-a92b-a2560006942b",
		Spec:              &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
		RemoveHealthCheck: true,
	}
}

func (f *deployFixture) resourceHealthCheck(t *testing.T) *deploymentv1.HealthCheckConfig {
	t.Helper()
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		t.Fatalf("decode resource spec: %v", err)
	}
	return spec.GetService().GetHealthCheck()
}

func deployedHealthPath(t *testing.T, f *deployFixture) string {
	t.Helper()
	health := desiredPayload(t, f).AppSpec.ServiceSpec.Deployment.HealthCheck
	if health == nil {
		return ""
	}
	return health.Path
}
