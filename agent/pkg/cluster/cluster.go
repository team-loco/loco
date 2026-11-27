package cluster

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/team-loco/loco/internal/workloadplatform"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
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

func (i *Inspector) Capabilities(ctx context.Context) (*agentv1.AgentCapabilities, error) {
	nodes, err := i.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	capabilities := &agentv1.AgentCapabilities{}
	for idx := range nodes.Items {
		node := &nodes.Items[idx]
		if platform, parseErr := workloadplatform.Parse(
			node.Status.NodeInfo.OperatingSystem + "/" + node.Status.NodeInfo.Architecture,
		); parseErr == nil {
			capabilities.Platforms = append(capabilities.Platforms, string(platform))
		}
	}

	slices.Sort(capabilities.GetPlatforms())
	capabilities.Platforms = slices.Compact(capabilities.GetPlatforms())

	return capabilities, nil
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
