package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	"google.golang.org/protobuf/proto"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func node(name, cpu, memory, pods string) *corev1.Node {
	return &corev1.Node{
		Name: name,
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
		Name: name, Namespace: "default",
		Spec: corev1.PodSpec{
			NodeName: "a",
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
		Name: "loco-controller", Namespace: "loco-system",
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func TestCapacity(t *testing.T) {
	nodeA := node("a", "4", "8Gi", "110")
	nodeB := node("b", "2", "4Gi", "110")
	running := pod("running", corev1.PodRunning, "500m", "1Gi")
	pending := pod("pending", corev1.PodPending, "250m", "512Mi")
	succeeded := pod("done", corev1.PodSucceeded, "1", "1Gi")
	client := fake.NewClientset(nodeA, nodeB, running, pending, succeeded)
	// The fake clientset ignores field selectors, so apply the one Capacity sends the way the API server does.
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		list, ok := action.(k8stesting.ListAction)
		if !ok {
			t.Fatalf("pod list action is %T", action)
		}
		restrictions := list.GetListRestrictions()
		if want := fields.ParseSelectorOrDie(activePodsSelector); restrictions.Fields.String() != want.String() {
			t.Errorf("pod field selector = %q, want %q", restrictions.Fields, want)
		}
		return true, &corev1.PodList{Items: []corev1.Pod{*running, *pending}}, nil
	})
	inspector := NewInspector(client, "loco-system", "loco-controller")

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
	if got := capacity.GetCpuMillicoresRequested(); got != 750 {
		t.Errorf("cpu used = %d, want 750", got)
	}
	if got := capacity.GetMemoryBytesRequested(); got != 1536<<20 {
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

func TestCapacityReportsMixedWorkerPlatforms(t *testing.T) {
	amd := node("amd", "2", "4Gi", "10")
	amd.Status.NodeInfo = corev1.NodeSystemInfo{OperatingSystem: "linux", Architecture: "amd64"}
	arm := node("arm", "2", "4Gi", "10")
	arm.Status.NodeInfo = corev1.NodeSystemInfo{OperatingSystem: "linux", Architecture: "arm64"}
	secondARM := arm.DeepCopy()
	secondARM.Name = "arm-2"
	inspector := NewInspector(fake.NewClientset(amd, arm, secondARM), "loco-system", "loco-controller")
	capacity, err := inspector.Capacity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(capacity.GetPlatforms(), ","); got != "linux/amd64,linux/arm64" {
		t.Fatalf("platforms %s", got)
	}
}

func TestCapacityCountsEffectiveAssignedRequests(t *testing.T) {
	t.Parallel()
	running := pod("running", corev1.PodRunning, "200m", "128Mi")
	running.Spec.InitContainers = []corev1.Container{{Name: "init", Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("800m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}}}
	running.Spec.Overhead = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	assigned := pod("starting", corev1.PodPending, "250m", "64Mi")
	waiting := pod("unassigned", corev1.PodPending, "10", "10Gi")
	waiting.Spec.NodeName = ""
	workspace, resourceID := uuid.NewString(), uuid.NewString()
	running.Labels = map[string]string{
		locolabels.LabelManagedBy:   locolabels.ControllerManagedBy,
		locolabels.LabelWorkspaceID: workspace,
		locolabels.LabelResourceID:  resourceID,
	}
	// Recreate the client after setting labels; fake clients copy their inputs.
	client := fake.NewClientset(node("a", "4", "8Gi", "110"), running, assigned, waiting)
	capacity, chunks, err := NewInspector(client, "loco-system", "loco-controller").CapacityReport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if capacity.GetCpuMillicoresRequested() != 1150 || capacity.GetMemoryBytesRequested() != 384<<20 {
		t.Fatalf(
			"effective requests = %dm CPU / %d bytes, want 1150m / 384Mi",
			capacity.GetCpuMillicoresRequested(),
			capacity.GetMemoryBytesRequested(),
		)
	}
	var tenantCPU, systemCPU int64
	for _, chunk := range chunks {
		for _, n := range chunk.GetNodes() {
			if n.GetCpuMillicoresRequested() != 1150 {
				t.Fatalf("node request lost init/overhead: %v", n)
			}
		}
		for _, a := range chunk.GetAllocations() {
			if a.GetKind() == "resource" {
				tenantCPU += a.GetCpuMillicores()
			} else {
				systemCPU += a.GetCpuMillicores()
			}
		}
	}
	if tenantCPU != 900 || systemCPU != 250 {
		t.Fatalf("node attribution: tenant=%d system=%d", tenantCPU, systemCPU)
	}
	if capacity.GetPodsAssigned() != 2 || capacity.GetPodsRunning() != 1 {
		t.Fatalf("assigned/running = %d/%d, want 2/1", capacity.GetPodsAssigned(), capacity.GetPodsRunning())
	}
}

func TestCapacityChunksStayBoundedWith100000ResourcesOnOneNode(t *testing.T) {
	t.Parallel()
	const selectedNode = "node"
	nodes := map[string]*clusterv1.NodeCapacity{selectedNode: {Name: selectedNode}}
	allocations := make(map[allocationKey]*agentv1.NodeAllocation, 100000)
	for range 100000 {
		key := allocationKey{
			node:      selectedNode,
			workspace: uuid.NewString(),
			resource:  uuid.NewString(),
			kind:      "resource",
		}
		allocations[key] = &agentv1.NodeAllocation{
			Node:          key.node,
			WorkspaceId:   key.workspace,
			ResourceId:    key.resource,
			Kind:          key.kind,
			CpuMillicores: 1,
			MemoryBytes:   1,
			Pods:          1,
		}
	}
	chunks := capacityChunks(nodes, allocations)
	var count int
	for index, chunk := range chunks {
		if proto.Size(chunk) > 4<<20 || len(chunk.GetAllocations()) > 1000 || chunk.GetSequence() != uint32(index) {
			t.Fatalf("unbounded/out-of-order chunk %d: %d bytes", index, proto.Size(chunk))
		}
		if chunk.GetComplete() != (index == len(chunks)-1) {
			t.Fatal("completion marker before final chunk")
		}
		count += len(chunk.GetAllocations())
	}
	if count != 100000 || len(chunks) != 101 {
		t.Fatalf("lost allocations: %d over %d chunks", count, len(chunks))
	}
	t.Logf(
		"100000 resources on one node -> %d chunks; largest allocation chunk %d bytes",
		len(chunks),
		proto.Size(chunks[1]),
	)
}

func TestCapacitySeparatesBuildAndSystemRequests(t *testing.T) {
	t.Parallel()
	build := pod("build", corev1.PodRunning, "1", "1Gi")
	build.Labels = map[string]string{
		locolabels.LabelManagedBy:   locolabels.ControllerManagedBy,
		locolabels.LabelWorkspaceID: uuid.NewString(),
		locolabels.LabelResourceID:  uuid.NewString(),
		locolabels.LabelBuildID:     uuid.NewString(),
	}
	system := pod("system", corev1.PodRunning, "100m", "128Mi")
	_, chunks, err := NewInspector(
		fake.NewClientset(node("a", "4", "8Gi", "110"), build, system),
		"loco-system",
		"loco-controller",
	).CapacityReport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, chunk := range chunks {
		for _, a := range chunk.GetAllocations() {
			got[a.GetKind()] += a.GetCpuMillicores()
		}
	}
	if got["build"] != 1000 || got["system"] != 100 || got["resource"] != 0 {
		t.Fatalf("attribution: %v", got)
	}
	build.Spec.NodeName = "vanished"
	_, _, err = NewInspector(
		fake.NewClientset(node("a", "4", "8Gi", "110"), build),
		"loco-system",
		"loco-controller",
	).CapacityReport(t.Context())
	if err == nil {
		t.Fatal("pod assigned to missing node silently omitted")
	}
}
