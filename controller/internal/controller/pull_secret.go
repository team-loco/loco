package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	workspacePullSecretName       = "loco-registry-pull"
	registryPullSecretRequeue     = 5 * time.Second
	messageRegistryPullSecretWait = "Waiting for the agent to write the registry pull secret..."
)

var errRegistryPullSecretPending = errors.New("the agent has not written the workspace's registry pull secret")

func (r *LocoResourceReconciler) copiesPullSecret() bool {
	return r.RegistryTokenMode || r.PullSecretName != ""
}

func (r *LocoResourceReconciler) pullSecretSource(locoRes *locov1alpha1.Application) client.ObjectKey {
	if r.RegistryTokenMode {
		name := locov1alpha1.RegistryPullSecretName(locoRes.Spec.WorkspaceID)
		return client.ObjectKey{Namespace: r.LocoNamespace, Name: name}
	}
	return client.ObjectKey{Namespace: r.LocoNamespace, Name: r.PullSecretName}
}

func (r *LocoResourceReconciler) ensureWorkspacePullSecret(
	ctx context.Context,
	locoRes *locov1alpha1.Application,
) error {
	namespace := r.workspaceNamespace(locoRes)
	key := client.ObjectKey{Namespace: namespace, Name: workspacePullSecretName}

	if !r.copiesPullSecret() {
		return r.deleteWorkspacePullSecret(ctx, key)
	}

	sourceKey := r.pullSecretSource(locoRes)
	source := &corev1.Secret{}
	err := r.Get(ctx, sourceKey, source)
	if r.RegistryTokenMode && apierrors.IsNotFound(err) {
		return fmt.Errorf("%w: %s", errRegistryPullSecretPending, sourceKey)
	}
	if err != nil {
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
	if obj.GetNamespace() != r.LocoNamespace {
		return false
	}
	if r.RegistryTokenMode {
		_, ok := obj.GetLabels()[locov1alpha1.LabelRegistryPull]
		return ok
	}
	return obj.GetName() == r.PullSecretName
}

func (r *LocoResourceReconciler) pullSecretChanged() predicate.Predicate {
	return predicate.NewPredicateFuncs(r.isPullSecret)
}

func (r *LocoResourceReconciler) applicationsForPullSecret(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	if !r.RegistryTokenMode {
		return r.applicationPerWorkspace(ctx, obj)
	}
	workspaceID := obj.GetLabels()[locov1alpha1.LabelRegistryPull]
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps); err != nil {
		slog.WarnContext(ctx, "failed to list applications for a registry pull secret change", "error", err)
		return nil
	}
	var requests []reconcile.Request
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.WorkspaceID != workspaceID || !app.DeletionTimestamp.IsZero() {
			continue
		}
		key := client.ObjectKeyFromObject(app)
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}
