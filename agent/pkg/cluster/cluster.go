package cluster

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	activePodsSelector = "status.phase!=Succeeded,status.phase!=Failed"
)

type Inspector struct {
	client               kubernetes.Interface
	controllerNamespace  string
	controllerDeployment string
	capacity             *capacityWatch
}

func NewInspector(client kubernetes.Interface, controllerNamespace, controllerDeployment string) *Inspector {
	return &Inspector{
		client:               client,
		controllerNamespace:  controllerNamespace,
		controllerDeployment: controllerDeployment,
	}
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
