package controller

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func (r *LocoResourceReconciler) ensureImagePullSecret(ctx context.Context, locoRes *locov1alpha1.Application) error {
	namespace := getNamespace(locoRes)
	secretName := getImageSecretName(locoRes)
	key := client.ObjectKey{Namespace: namespace, Name: secretName}

	if r.pullSecretName == "" {
		return r.deleteImagePullSecret(ctx, key)
	}

	sourceKey := client.ObjectKey{Namespace: r.locoNamespace, Name: r.pullSecretName}
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

	slog.DebugContext(ctx, "ensuring image pull secret", "namespace", namespace, "name", secretName)

	labels := managedLabels(locoRes)
	annotations := ownerAnnotations(locoRes)
	data := map[string][]byte{corev1.DockerConfigJsonKey: dockerConfig}
	secret := corev1ac.Secret(secretName, namespace).
		WithLabels(labels).
		WithAnnotations(annotations).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(data)

	opts := applyOptions()
	if err := r.Apply(ctx, secret, opts...); err != nil {
		return fmt.Errorf("apply image pull secret %s: %w", key, err)
	}
	return nil
}

func (r *LocoResourceReconciler) deleteImagePullSecret(ctx context.Context, key client.ObjectKey) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get image pull secret %s: %w", key, err)
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete image pull secret %s: %w", key, err)
	}
	return nil
}

func (r *LocoResourceReconciler) isPullSecret(obj client.Object) bool {
	return obj.GetNamespace() == r.locoNamespace && obj.GetName() == r.pullSecretName
}

func (r *LocoResourceReconciler) pullSecretChanged() predicate.Predicate {
	return predicate.NewPredicateFuncs(r.isPullSecret)
}

func (r *LocoResourceReconciler) allApplications(ctx context.Context, _ client.Object) []reconcile.Request {
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps); err != nil {
		slog.ErrorContext(ctx, "failed to list applications for registry pull secret change", "error", err)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(apps.Items))
	for i := range apps.Items {
		app := &apps.Items[i]
		key := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}
