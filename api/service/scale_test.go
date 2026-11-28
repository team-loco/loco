package service

import (
	"testing"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func scaledSpec(minReplicas, maxReplicas int32) *deploymentv1.RegionDeploymentTarget {
	cpu := testRegionCPU
	memory := testRegionMemory
	return &deploymentv1.RegionDeploymentTarget{
		Cpu:         &cpu,
		Memory:      &memory,
		MinReplicas: &minReplicas,
		MaxReplicas: &maxReplicas,
	}
}

func TestApplyScaleSetsTheReplicasTheControllerReads(t *testing.T) {
	t.Parallel()
	replicas := int32(3)
	spec := scaledSpec(1, 2)
	applyScale(&resourcev1.ScaleResourceRequest{Replicas: &replicas}, spec)
	if spec.GetMinReplicas() != replicas || spec.GetMaxReplicas() != replicas {
		t.Errorf("spec replicas %d-%d, want %d-%d", spec.GetMinReplicas(), spec.GetMaxReplicas(), replicas, replicas)
	}

	lower := int32(2)
	spec = scaledSpec(1, 5)
	applyScale(&resourcev1.ScaleResourceRequest{Replicas: &lower}, spec)
	if spec.GetMinReplicas() != lower || spec.GetMaxReplicas() != lower {
		t.Errorf("spec replicas %d-%d, want %d-%d", spec.GetMinReplicas(), spec.GetMaxReplicas(), lower, lower)
	}
}

func TestApplyScaleKeepsUnrequestedValues(t *testing.T) {
	t.Parallel()
	cpu := "500m"
	spec := scaledSpec(2, 2)
	applyScale(&resourcev1.ScaleResourceRequest{Cpu: &cpu}, spec)
	if spec.GetMinReplicas() != 2 || spec.GetMaxReplicas() != 2 {
		t.Errorf("spec replicas %d-%d, want 2-2", spec.GetMinReplicas(), spec.GetMaxReplicas())
	}
	if spec.GetCpu() != cpu || spec.GetMemory() != testRegionMemory {
		t.Errorf("cpu %q, memory %q, want %q and 64Mi", spec.GetCpu(), spec.GetMemory(), cpu)
	}
}

func TestScaleResourceRequestNeedsAnEnvironment(t *testing.T) {
	t.Parallel()
	replicas := int32(2)
	cases := map[string]string{
		"missing":    "",
		"not a uuid": stagingName,
	}
	for name, environmentID := range cases {
		t.Run(name, func(t *testing.T) {
			req := &resourcev1.ScaleResourceRequest{
				ResourceId:    uuid.NewString(),
				EnvironmentId: environmentID,
				Replicas:      &replicas,
			}
			if err := protovalidate.Validate(req); err == nil {
				t.Fatalf("environment_id %q passed validation", environmentID)
			}
		})
	}

	valid := &resourcev1.ScaleResourceRequest{
		ResourceId:    uuid.NewString(),
		EnvironmentId: uuid.NewString(),
		Replicas:      &replicas,
	}
	if err := protovalidate.Validate(valid); err != nil {
		t.Fatalf("a request naming an environment failed validation: %v", err)
	}
}

func TestManualScaleDisablesAutoscaling(t *testing.T) {
	t.Parallel()
	spec := scaledSpec(1, 5)
	spec.Scalers = &deploymentv1.Scalers{Enabled: true, CpuTarget: new(int32(70))}
	cpu := "500m"
	applyScale(&resourcev1.ScaleResourceRequest{Cpu: &cpu}, spec)
	if !spec.GetScalers().GetEnabled() {
		t.Fatal("CPU-only change disabled autoscaling")
	}
	request := &resourcev1.ScaleResourceRequest{Replicas: new(int32(3))}
	if !scaleChangesDeployment(request, spec, 3) {
		t.Fatal("fixed scale at the live count must disable autoscaling")
	}
	applyScale(request, spec)
	if spec.GetScalers().GetEnabled() || spec.GetMinReplicas() != 3 || spec.GetMaxReplicas() != 3 {
		t.Fatalf("fixed scale = %v", spec)
	}
}
