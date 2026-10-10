package controller

import (
	"context"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func CacheOptions(locoNamespace, pullSecretName string) cache.Options {
	managedSet := labels.Set(managed.Labels())
	managedSelector := labels.SelectorFromSet(managedSet)
	managedObjects := cache.ByObject{Label: managedSelector}
	secrets := managedObjects
	if locoNamespace != "" && pullSecretName != "" {
		everything := labels.Everything()
		pullSecretSelector := fields.OneTermEqualSelector("metadata.name", pullSecretName)
		secrets = cache.ByObject{
			Label: managedSelector,
			Namespaces: map[string]cache.Config{
				locoNamespace:       {LabelSelector: everything, FieldSelector: pullSecretSelector},
				cache.AllNamespaces: {},
			},
		}
	}
	podSet := labels.Set{
		managed.LabelManagedBy: managed.ManagedByValue,
		managed.LabelComponent: componentApplication,
	}
	podSelector := labels.SelectorFromSet(podSet)
	applicationPods := cache.ByObject{Label: podSelector}
	stripManagedFields := cache.TransformStripManagedFields()
	return cache.Options{
		DefaultTransform: stripManagedFields,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Namespace{}:      managedObjects,
			&corev1.Secret{}:         secrets,
			&corev1.Service{}:        managedObjects,
			&corev1.ServiceAccount{}: managedObjects,
			&rbacv1.Role{}:           managedObjects,
			&rbacv1.RoleBinding{}:    managedObjects,
			&appsv1.Deployment{}:     managedObjects,
			&corev1.Pod{}:            applicationPods,
		},
	}
}

func applicationForObject(_ context.Context, obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	namespace, ok := annotations[annotationAppNamespace]
	if !ok || namespace == "" {
		return nil
	}
	name, ok := annotations[annotationAppName]
	if !ok || name == "" {
		return nil
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	return []reconcile.Request{{NamespacedName: key}}
}

func (r *LocoResourceReconciler) applicationPerWorkspace(ctx context.Context, _ client.Object) []reconcile.Request {
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps); err != nil {
		slog.WarnContext(ctx, "failed to list applications for a workspace-wide change", "error", err)
		return nil
	}
	seen := make(map[string]bool, len(apps.Items))
	var requests []reconcile.Request
	for i := range apps.Items {
		app := &apps.Items[i]
		if seen[app.Spec.WorkspaceID] || !app.DeletionTimestamp.IsZero() {
			continue
		}
		if err := app.Spec.Validate(); err != nil {
			continue
		}
		seen[app.Spec.WorkspaceID] = true
		key := client.ObjectKeyFromObject(app)
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}
