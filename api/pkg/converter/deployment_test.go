package converter

import (
	"testing"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testRegion = "us-east-1"

func mergeReplicas(t *testing.T, request, region [2]int32, defaults [2]int32) (int32, int32) {
	t.Helper()
	target := &resourcev1.RegionTarget{Enabled: true, MinReplicas: region[0], MaxReplicas: region[1]}
	regions := map[string]*resourcev1.RegionTarget{testRegion: target}
	resourceService := &resourcev1.ServiceSpec{Regions: regions}
	resourceSpec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: resourceService}}

	requestService := &deploymentv1.ServiceDeploymentSpec{}
	if request[0] != 0 {
		requestService.MinReplicas = &request[0]
	}
	if request[1] != 0 {
		requestService.MaxReplicas = &request[1]
	}
	requestSpec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: requestService}}

	serviceDefaults := servicedefaults.Defaults{
		CPU:         "100m",
		Memory:      "256Mi",
		MinReplicas: defaults[0],
		MaxReplicas: defaults[1],
	}
	merged, err := MergeDeploymentSpec(resourceSpec, requestSpec, testRegion, serviceDefaults)
	if err != nil {
		t.Fatalf("MergeDeploymentSpec: %v", err)
	}
	service := merged.GetService()
	return service.GetMinReplicas(), service.GetMaxReplicas()
}

func TestMergeDeploymentSpecResolvesReplicas(t *testing.T) {
	cases := map[string]struct {
		request, region, defaults [2]int32
		wantMin, wantMax          int32
	}{
		"request min above the default max raises max": {[2]int32{3, 0}, [2]int32{0, 0}, [2]int32{1, 1}, 3, 3},
		"region min above the default max raises max":  {[2]int32{0, 0}, [2]int32{4, 0}, [2]int32{1, 2}, 4, 4},
		"default max above the min is kept":            {[2]int32{2, 0}, [2]int32{0, 0}, [2]int32{1, 5}, 2, 5},
		"only defaults":                                {[2]int32{0, 0}, [2]int32{0, 0}, [2]int32{2, 3}, 2, 3},
		"explicit request max is kept":                 {[2]int32{3, 2}, [2]int32{0, 0}, [2]int32{1, 1}, 3, 2},
		"explicit region max is kept":                  {[2]int32{3, 0}, [2]int32{0, 1}, [2]int32{1, 5}, 3, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			gotMin, gotMax := mergeReplicas(t, tc.request, tc.region, tc.defaults)
			if gotMin != tc.wantMin || gotMax != tc.wantMax {
				t.Errorf("replicas = %d-%d, want %d-%d", gotMin, gotMax, tc.wantMin, tc.wantMax)
			}
		})
	}
}
