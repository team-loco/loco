package converter

import (
	"testing"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	testRegion = "us-east-1"
	testCPU    = "100m"
	testMemory = "256Mi"
	westRegion = "eu-west"
	eastRegion = "us-east"
)

func mergeReplicas(t *testing.T, request, region [2]int32, defaults [2]int32) (int32, int32) {
	t.Helper()
	target := &resourcev1.RegionTarget{Enabled: true, MinReplicas: region[0], MaxReplicas: region[1]}
	regions := map[string]*resourcev1.RegionTarget{testRegion: target}
	resourceService := &resourcev1.ServiceSpec{Regions: regions}
	resourceSpec := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: resourceService}}

	override := &deploymentv1.RegionDeploymentTarget{}
	if request[0] != 0 {
		override.MinReplicas = &request[0]
	}
	if request[1] != 0 {
		override.MaxReplicas = &request[1]
	}
	requestService := &deploymentv1.ServiceDeploymentSpec{
		Regions: map[string]*deploymentv1.RegionDeploymentTarget{testRegion: override},
	}
	requestSpec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: requestService}}

	serviceDefaults := servicedefaults.Defaults{
		CPU:         testCPU,
		Memory:      testMemory,
		MinReplicas: defaults[0],
		MaxReplicas: defaults[1],
	}
	spec, err := MergeDeploymentSpec(resourceSpec, requestSpec, []string{testRegion}, serviceDefaults)
	if err != nil {
		t.Fatalf("MergeDeploymentSpec: %v", err)
	}
	merged := spec.GetService().GetRegions()[testRegion]
	return merged.GetMinReplicas(), merged.GetMaxReplicas()
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

func TestMergeDeploymentSpecSizesEveryTargetRegion(t *testing.T) {
	regions := map[string]*resourcev1.RegionTarget{
		westRegion: {Enabled: true, Cpu: "250m", MinReplicas: 2},
		eastRegion: {Enabled: true, Memory: "1Gi", MinReplicas: 3},
		"ap-east":  {Enabled: true},
		"off":      {Enabled: false},
	}
	resource := &resourcev1.ResourceSpec{
		Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{Regions: regions}},
	}
	override := &deploymentv1.RegionDeploymentTarget{MinReplicas: new(int32(5))}
	request := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{
		Service: &deploymentv1.ServiceDeploymentSpec{
			Regions: map[string]*deploymentv1.RegionDeploymentTarget{eastRegion: override},
		},
	}}
	defaults := servicedefaults.Defaults{CPU: testCPU, Memory: testMemory, MinReplicas: 1, MaxReplicas: 1}
	merged, err := MergeDeploymentSpec(resource, request, []string{westRegion, eastRegion}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	got := merged.GetService().GetRegions()
	if len(got) != 2 {
		t.Fatalf("regions = %v, want eu-west and us-east only", got)
	}
	if eu := got[westRegion]; eu.GetCpu() != "250m" || eu.GetMemory() != testMemory || eu.GetMinReplicas() != 2 {
		t.Errorf("eu-west = %v, want the region target over the defaults", eu)
	}
	if us := got[eastRegion]; us.GetCpu() != testCPU || us.GetMemory() != "1Gi" || us.GetMinReplicas() != 5 {
		t.Errorf("us-east = %v, want the request override over the region target", us)
	}

	for name, targets := range map[string][]string{
		"disabled region": {"off"},
		"unknown region":  {"mars"},
		"untargeted size": {westRegion},
	} {
		if _, err := MergeDeploymentSpec(resource, request, targets, defaults); err == nil {
			t.Errorf("%s: merge succeeded", name)
		}
	}
}

func TestRuntimeUserSurvivesDeploymentMerge(t *testing.T) {
	resource := &resourcev1.ResourceSpec{
		Spec: &resourcev1.ResourceSpec_Service{
			Service: &resourcev1.ServiceSpec{Regions: map[string]*resourcev1.RegionTarget{testRegion: {Enabled: true}}},
		},
	}
	for _, id := range []*int64{nil, new(int64(1654))} {
		request := &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{
				Service: &deploymentv1.ServiceDeploymentSpec{RunAsUser: id, RunAsGroup: id},
			},
		}
		merged, err := MergeDeploymentSpec(resource, request, []string{testRegion}, servicedefaults.Defaults{})
		if err != nil {
			t.Fatal(err)
		}
		controller := ProtoToServiceDeploymentSpec(merged)
		if id == nil {
			if controller.RunAsUser != nil || controller.RunAsGroup != nil {
				t.Fatalf("unexpected override: %+v", controller)
			}
		} else if controller.RunAsUser == nil || *controller.RunAsUser != *id ||
			controller.RunAsGroup == nil || *controller.RunAsGroup != *id {
			t.Fatalf("override lost: %+v", controller)
		}
	}
}

func TestObservabilityUsesResourceSettingsAndExplicitDeploymentOverrides(t *testing.T) {
	resource := &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
		Observability: &deploymentv1.ObservabilityConfig{InjectOtel: new(true)},
		Regions:       map[string]*resourcev1.RegionTarget{testRegion: {Enabled: true}},
	}}}
	for _, inject := range []bool{true, false} {
		request := &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Observability: &deploymentv1.ObservabilityConfig{
					InjectOtel: new(inject),
				},
			}},
		}
		merged, err := MergeDeploymentSpec(resource, request, []string{testRegion}, servicedefaults.Defaults{})
		if err != nil {
			t.Fatal(err)
		}
		settings := merged.GetService().GetObservability()
		if settings.InjectOtel == nil || settings.GetInjectOtel() != inject {
			t.Fatalf("snapshot=%v", settings)
		}
		if got := ProtoToServiceDeploymentSpec(merged).Observability.InjectOtel; got != inject {
			t.Fatalf("pod SDK injection=%v want=%v", got, inject)
		}
	}
}
