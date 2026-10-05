package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func CacheOptions() cache.Options {
	managedSet := labels.Set{labelManagedBy: managedByValue}
	managedSelector := labels.SelectorFromSet(managedSet)
	managed := cache.ByObject{Label: managedSelector}
	stripManagedFields := cache.TransformStripManagedFields()
	return cache.Options{
		DefaultTransform: stripManagedFields,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Namespace{}:      managed,
			&corev1.Secret{}:         managed,
			&corev1.Service{}:        managed,
			&corev1.ServiceAccount{}: managed,
			&rbacv1.Role{}:           managed,
			&rbacv1.RoleBinding{}:    managed,
			&appsv1.Deployment{}:     managed,
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
