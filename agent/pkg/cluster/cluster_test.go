package cluster

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func node(name, architecture string) *corev1.Node {
	return &corev1.Node{
		Name: name,
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{OperatingSystem: "linux", Architecture: architecture},
		},
	}
}

func controller(ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		Name: "loco-controller", Namespace: "loco-system",
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
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
			inspector := NewInspector(client, "loco-system", "loco-controller")

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

func TestCapabilitiesReportMixedWorkerPlatforms(t *testing.T) {
	amd := node("amd", "amd64")
	arm := node("arm", "arm64")
	secondARM := arm.DeepCopy()
	secondARM.Name = "arm-2"
	inspector := NewInspector(fake.NewClientset(amd, arm, secondARM), "loco-system", "loco-controller")
	capabilities, err := inspector.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(capabilities.GetPlatforms(), ","); got != "linux/amd64,linux/arm64" {
		t.Fatalf("platforms %s", got)
	}
}
