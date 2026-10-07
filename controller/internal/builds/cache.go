package builds

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func CacheOptions(namespace string) cache.Options {
	managedSet := labels.Set(managed.Labels())
	managedSelector := labels.SelectorFromSet(managedSet)
	namespaceOnly := map[string]cache.Config{namespace: {}}
	managedObjects := cache.ByObject{Label: managedSelector, Namespaces: namespaceOnly}
	builds := cache.ByObject{Namespaces: namespaceOnly}
	stripManagedFields := cache.TransformStripManagedFields()
	return cache.Options{
		DefaultTransform: stripManagedFields,
		ByObject: map[client.Object]cache.ByObject{
			&batchv1.Job{}:        managedObjects,
			&corev1.Pod{}:         managedObjects,
			&locov1alpha1.Build{}: builds,
		},
	}
}
