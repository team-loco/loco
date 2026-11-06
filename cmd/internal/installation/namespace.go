package installation

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	workerPodSecurityPolicy  = "restricted"
	workerPodSecurityVersion = "latest"
)

// This fixed Kubernetes policy mirrors manifests/namespaces/loco-system.yaml;
// the CLI must carry it without requiring an operator's source checkout.
func workerNamespaceLabels() map[string]string {
	return map[string]string{
		"pod-security.kubernetes.io/enforce":         workerPodSecurityPolicy,
		"pod-security.kubernetes.io/enforce-version": workerPodSecurityVersion,
		"pod-security.kubernetes.io/audit":           workerPodSecurityPolicy,
		"pod-security.kubernetes.io/audit-version":   workerPodSecurityVersion,
		"pod-security.kubernetes.io/warn":            workerPodSecurityPolicy,
		"pod-security.kubernetes.io/warn-version":    workerPodSecurityVersion,
	}
}

func (k *Kubernetes) checkWorkerNamespace(ctx context.Context) error {
	namespace, err := k.Core.Namespaces().Get(ctx, k.Target.Namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return safeKubernetesError("read worker namespace policy", err)
	}
	for key, value := range workerNamespaceLabels() {
		if namespace.Labels[key] != value {
			return errWorkerNamespacePolicy
		}
	}
	return nil
}
