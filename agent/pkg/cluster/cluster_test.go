package cluster

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func node(name, cpu, memory, pods string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(memory),
				corev1.ResourcePods:   resource.MustParse(pods),
			},
		},
	}
}

func pod(name string, phase corev1.PodPhase, cpu, memory string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "app",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpu),
						corev1.ResourceMemory: resource.MustParse(memory),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func controller(ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "controller-loco-manager", Namespace: "loco-system"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func TestCapacity(t *testing.T) {
	nodeA := node("a", "4", "8Gi", "110")
	nodeB := node("b", "2", "4Gi", "110")
	running := pod("running", corev1.PodRunning, "500m", "1Gi")
	pending := pod("pending", corev1.PodPending, "250m", "512Mi")
	succeeded := pod("done", corev1.PodSucceeded, "1", "1Gi")
	client := fake.NewClientset(nodeA, nodeB, running, pending, succeeded)
	inspector := NewInspector(client, "loco-system", "controller-loco-manager")

	capacity, err := inspector.Capacity(context.Background())
	if err != nil {
		t.Fatalf("Capacity: %v", err)
	}

	if got := capacity.GetCpuMillicoresTotal(); got != 6000 {
		t.Errorf("cpu total = %d, want 6000", got)
	}
	if got := capacity.GetMemoryBytesTotal(); got != 12<<30 {
		t.Errorf("memory total = %d, want %d", got, int64(12<<30))
	}
	if got := capacity.GetPodsTotal(); got != 220 {
		t.Errorf("pods total = %d, want 220", got)
	}
	if got := capacity.GetCpuMillicoresUsed(); got != 750 {
		t.Errorf("cpu used = %d, want 750", got)
	}
	if got := capacity.GetMemoryBytesUsed(); got != 1536<<20 {
		t.Errorf("memory used = %d, want %d", got, int64(1536<<20))
	}
	if got := capacity.GetPodsRunning(); got != 1 {
		t.Errorf("pods running = %d, want 1", got)
	}
}

func TestHealth(t *testing.T) {
	cases := []struct {
		name           string
		deployment     *appsv1.Deployment
		wantController bool
	}{
		{name: "ready controller", deployment: controller(1), wantController: true},
		{name: "no ready replicas", deployment: controller(0), wantController: false},
		{name: "missing controller", deployment: nil, wantController: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			if tc.deployment != nil {
				client = fake.NewClientset(tc.deployment)
			}
			inspector := NewInspector(client, "loco-system", "controller-loco-manager")

			health := inspector.Health(context.Background())

			if !health.GetKubernetesHealthy() {
				t.Errorf("kubernetes unhealthy: %s", health.GetMessage())
			}
			if health.GetControllerHealthy() != tc.wantController {
				t.Errorf("controller healthy = %v, want %v", health.GetControllerHealthy(), tc.wantController)
			}
			if tc.wantController && health.GetMessage() != "" {
				t.Errorf("message = %q, want empty", health.GetMessage())
			}
			if !tc.wantController && health.GetMessage() == "" {
				t.Error("message is empty for an unhealthy controller")
			}
		})
	}
}
