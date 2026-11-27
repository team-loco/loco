package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/workloadplatform"
	locolabels "github.com/team-loco/loco/k8sapi/labels"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	podresource "k8s.io/component-helpers/resource"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	activePodsSelector = "status.phase!=Succeeded,status.phase!=Failed"
)

type Inspector struct {
	client               kubernetes.Interface
	controllerNamespace  string
	controllerDeployment string
}

func NewInspector(client kubernetes.Interface, controllerNamespace, controllerDeployment string) *Inspector {
	return &Inspector{
		client:               client,
		controllerNamespace:  controllerNamespace,
		controllerDeployment: controllerDeployment,
	}
}

func (i *Inspector) Capacity(ctx context.Context) (*agentv1.AgentCapacity, error) {
	capacity, _, err := i.CapacityReport(ctx)
	return capacity, err
}

// CapacityReport collects one set of node and pod observations for totals and attribution.
// Kubernetes does not offer an atomic cross-kind read; pods assigned to vanished nodes
// invalidate this sample rather than silently disappearing from its totals.
func (i *Inspector) CapacityReport(ctx context.Context) (*agentv1.AgentCapacity, []*agentv1.CapacityReport, error) {
	nodes, err := i.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}

	capacity := &agentv1.AgentCapacity{}
	byNode := make(map[string]*clusterv1.NodeCapacity, len(nodes.Items))
	allocations := make(map[allocationKey]*agentv1.NodeAllocation)
	for idx := range nodes.Items {
		node := &nodes.Items[idx]
		if platform, parseErr := workloadplatform.Parse(
			node.Status.NodeInfo.OperatingSystem + "/" + node.Status.NodeInfo.Architecture,
		); parseErr == nil {
			capacity.Platforms = append(capacity.Platforms, string(platform))
		}
		allocatable := node.Status.Allocatable
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady {
				ready = condition.Status == corev1.ConditionTrue
			}
		}
		byNode[node.Name] = &clusterv1.NodeCapacity{
			Name:          node.Name,
			Ready:         ready,
			Unschedulable: node.Spec.Unschedulable,
			CpuMillicoresAllocatable: allocatable.Cpu().
				MilliValue(),
			MemoryBytesAllocatable: allocatable.Memory().Value(),
		}
		capacity.CpuMillicoresTotal += allocatable.Cpu().MilliValue()
		capacity.MemoryBytesTotal += allocatable.Memory().Value()
		capacity.PodsTotal += int32(allocatable.Pods().Value())
	}

	slices.Sort(capacity.GetPlatforms())
	capacity.Platforms = slices.Compact(capacity.GetPlatforms())

	pods, err := i.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: activePodsSelector,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list pods: %w", err)
	}

	for idx := range pods.Items {
		pod := &pods.Items[idx]
		if pod.Spec.NodeName == "" {
			continue
		}
		n, exists := byNode[pod.Spec.NodeName]
		if !exists {
			return nil, nil, fmt.Errorf(
				"pod %s/%s is assigned to an unobserved node %s",
				pod.Namespace,
				pod.Name,
				pod.Spec.NodeName,
			)
		}
		capacity.PodsAssigned++
		if pod.Status.Phase == corev1.PodRunning {
			capacity.PodsRunning++
		}
		requests := podresource.PodRequests(pod, podresource.PodResourcesOptions{})
		capacity.CpuMillicoresRequested += requests.Cpu().MilliValue()
		capacity.MemoryBytesRequested += requests.Memory().Value()
		n.CpuMillicoresRequested += requests.Cpu().MilliValue()
		n.MemoryBytesRequested += requests.Memory().Value()
		key, err := podAllocation(pod)
		if err != nil {
			return nil, nil, err
		}
		allocation := allocations[key]
		if allocation == nil {
			allocation = &agentv1.NodeAllocation{
				Node:          key.node,
				WorkspaceId:   key.workspace,
				ResourceId:    key.resource,
				Kind:          key.kind,
				EnvironmentId: key.environment,
				BuildId:       key.build,
			}
			allocations[key] = allocation
		}
		allocation.CpuMillicores += requests.Cpu().MilliValue()
		allocation.MemoryBytes += requests.Memory().Value()
		allocation.Pods++
	}

	return capacity, capacityChunks(byNode, allocations), nil
}

func (i *Inspector) Health(ctx context.Context) *agentv1.AgentHealth {
	health := &agentv1.AgentHealth{
		KubernetesHealthy: true,
		ControllerHealthy: true,
	}
	var problems []string

	if _, err := i.client.Discovery().ServerVersion(); err != nil {
		health.KubernetesHealthy = false
		problem := fmt.Sprintf("kubernetes api unreachable: %v", err)
		problems = append(problems, problem)
	}

	if err := i.controllerReady(ctx); err != nil {
		health.ControllerHealthy = false
		problems = append(problems, err.Error())
	}

	health.Message = strings.Join(problems, "; ")
	return health
}

func (i *Inspector) controllerReady(ctx context.Context) error {
	ref := i.controllerNamespace + "/" + i.controllerDeployment
	deployments := i.client.AppsV1().Deployments(i.controllerNamespace)
	deployment, err := deployments.Get(ctx, i.controllerDeployment, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get controller deployment %s: %w", ref, err)
	}
	if deployment.Status.ReadyReplicas < 1 {
		return fmt.Errorf("controller deployment %s has no ready replicas", ref)
	}
	return nil
}

func (i *Inspector) Key(ctx context.Context) (string, error) {
	namespace, err := i.client.CoreV1().Namespaces().Get(ctx, metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read cluster identity: %w", err)
	}
	return string(namespace.UID), nil
}

type allocationKey struct{ node, workspace, resource, kind, environment, build string }

func podAllocation(pod *corev1.Pod) (allocationKey, error) {
	key := allocationKey{node: pod.Spec.NodeName, kind: "system"}
	if pod.Labels[locolabels.LabelManagedBy] != locolabels.ControllerManagedBy {
		return key, nil
	}
	key.workspace, key.resource = pod.Labels[locolabels.LabelWorkspaceID], pod.Labels[locolabels.LabelResourceID]
	identities := []string{key.workspace, key.resource}
	key.build = pod.Labels[locolabels.LabelBuildID]
	if key.build != "" {
		key.kind = "build"
		identities = append(identities, key.build)
	} else {
		key.kind = "resource"
		key.environment = pod.Labels[locolabels.LabelEnvironmentID]
		identities = append(identities, key.environment)
	}
	for _, raw := range identities {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return allocationKey{}, fmt.Errorf(
				"managed pod %s/%s has an invalid allocation identity",
				pod.Namespace,
				pod.Name,
			)
		}
	}
	return key, nil
}

// Each entry has bounded names/IDs and at most 1000 entries fit well below the
// transport's 4 MiB limit, even when every resource shares one node.
const capacityChunkEntries = 1000

func capacityChunks(
	nodes map[string]*clusterv1.NodeCapacity,
	allocations map[allocationKey]*agentv1.NodeAllocation,
) []*agentv1.CapacityReport {
	id := uuid.NewString()
	chunks := []*agentv1.CapacityReport{}
	chunk := &agentv1.CapacityReport{}
	flush := func() {
		chunk.Id, chunk.Sequence = id, uint32(len(chunks))
		chunk.NodeCount, chunk.AllocationCount = int32(len(nodes)), int32(len(allocations))
		chunks = append(chunks, chunk)
		chunk = &agentv1.CapacityReport{}
	}
	// Nodes precede allocations because allocation rows refer to their node.
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		chunk.Nodes = append(chunk.Nodes, nodes[name])
		if len(chunk.GetNodes()) == capacityChunkEntries {
			flush()
		}
	}
	if len(chunk.GetNodes()) > 0 {
		flush()
	}
	for _, allocation := range allocations {
		chunk.Allocations = append(chunk.Allocations, allocation)
		if len(chunk.GetAllocations()) == capacityChunkEntries {
			flush()
		}
	}
	if len(chunk.GetAllocations()) > 0 || len(chunks) == 0 {
		flush()
	}
	chunks[len(chunks)-1].Complete = true
	return chunks
}
