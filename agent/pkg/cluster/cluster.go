package cluster

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const activePodsSelector = "status.phase!=Succeeded,status.phase!=Failed"

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
	nodes, err := i.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	capacity := &agentv1.AgentCapacity{}
	for idx := range nodes.Items {
		allocatable := nodes.Items[idx].Status.Allocatable
		capacity.CpuMillicoresTotal += allocatable.Cpu().MilliValue()
		capacity.MemoryBytesTotal += allocatable.Memory().Value()
		capacity.PodsTotal += int32(allocatable.Pods().Value())
	}

	pods, err := i.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: activePodsSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}

	for idx := range pods.Items {
		pod := &pods.Items[idx]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if pod.Status.Phase == corev1.PodRunning {
			capacity.PodsRunning++
		}
		for cIdx := range pod.Spec.Containers {
			requests := pod.Spec.Containers[cIdx].Resources.Requests
			capacity.CpuMillicoresUsed += requests.Cpu().MilliValue()
			capacity.MemoryBytesUsed += requests.Memory().Value()
		}
	}

	return capacity, nil
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
