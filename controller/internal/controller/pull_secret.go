package controller

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const workspacePullSecretName = "loco-registry-pull"

func (r *LocoResourceReconciler) ensureWorkspacePullSecret(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	namespace := getNamespace(locoRes)
	key := client.ObjectKey{Namespace: namespace, Name: workspacePullSecretName}

	if r.PullSecretName == "" {
		return r.deleteWorkspacePullSecret(ctx, key)
	}

	sourceKey := client.ObjectKey{Namespace: r.LocoNamespace, Name: r.PullSecretName}
	source := &corev1.Secret{}
	if err := r.Get(ctx, sourceKey, source); err != nil {
		return fmt.Errorf("get registry pull secret %s: %w", sourceKey, err)
	}
	if source.Type != corev1.SecretTypeDockerConfigJson {
		return fmt.Errorf(
			"registry pull secret %s has type %s, want %s",
			sourceKey,
			source.Type,
			corev1.SecretTypeDockerConfigJson,
		)
	}
	dockerConfig, ok := source.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return fmt.Errorf("registry pull secret %s has no %s key", sourceKey, corev1.DockerConfigJsonKey)
	}

	slog.DebugContext(ctx, "ensuring workspace pull secret", "namespace", namespace, "name", workspacePullSecretName)

	labels := workspaceObjectLabels(locoRes)
	data := map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig}
	secret := corev1ac.Secret(workspacePullSecretName, namespace).
		WithLabels(labels).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(data)

	opts := managed.ApplyOptions()
	if err := r.Apply(ctx, secret, opts...); err != nil {
		return fmt.Errorf("apply workspace pull secret %s: %w", key, err)
	}
	return nil
}

func (r *LocoResourceReconciler) deleteWorkspacePullSecret(ctx context.Context, key client.ObjectKey) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get workspace pull secret %s: %w", key, err)
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete workspace pull secret %s: %w", key, err)
	}
	return nil
}

func (r *LocoResourceReconciler) isPullSecret(obj client.Object) bool {
	return obj.GetNamespace() == r.LocoNamespace && obj.GetName() == r.PullSecretName
}

func (r *LocoResourceReconciler) pullSecretChanged() predicate.Predicate {
	return predicate.NewPredicateFuncs(r.isPullSecret)
}
