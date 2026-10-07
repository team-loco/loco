package service

import (
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func scaledSpec(minReplicas, maxReplicas int32) *deploymentv1.ServiceDeploymentSpec {
	cpu := "100m"
	memory := testRegionMemory
	return &deploymentv1.ServiceDeploymentSpec{
		Cpu:         &cpu,
		Memory:      &memory,
		MinReplicas: &minReplicas,
		MaxReplicas: &maxReplicas,
	}
}

func TestApplyScaleSetsTheReplicasTheControllerReads(t *testing.T) {
	replicas := int32(3)
	spec := scaledSpec(1, 2)
	got := applyScale(&resourcev1.ScaleResourceRequest{Replicas: &replicas}, spec, 1)
	if got != replicas {
		t.Errorf("replicas = %d, want %d", got, replicas)
	}
	if spec.GetMinReplicas() != replicas || spec.GetMaxReplicas() != replicas {
		t.Errorf("spec replicas %d-%d, want %d-%d", spec.GetMinReplicas(), spec.GetMaxReplicas(), replicas, replicas)
	}

	lower := int32(2)
	spec = scaledSpec(1, 5)
	applyScale(&resourcev1.ScaleResourceRequest{Replicas: &lower}, spec, 1)
	if spec.GetMinReplicas() != lower || spec.GetMaxReplicas() != 5 {
		t.Errorf("spec replicas %d-%d, want %d-5", spec.GetMinReplicas(), spec.GetMaxReplicas(), lower)
	}
}

func TestApplyScaleKeepsUnrequestedValues(t *testing.T) {
	cpu := "500m"
	spec := scaledSpec(2, 2)
	got := applyScale(&resourcev1.ScaleResourceRequest{Cpu: &cpu}, spec, 2)
	if got != 2 || spec.GetMinReplicas() != 2 {
		t.Errorf("replicas %d, spec min %d, want 2 and 2", got, spec.GetMinReplicas())
	}
	if spec.GetCpu() != cpu || spec.GetMemory() != testRegionMemory {
		t.Errorf("cpu %q, memory %q, want %q and 64Mi", spec.GetCpu(), spec.GetMemory(), cpu)
	}
}
