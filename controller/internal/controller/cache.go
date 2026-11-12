package controller

import (
	"context"
	"fmt"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/team-loco/loco/controller/internal/managed"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func CacheOptions(locoNamespace, pullSecretName string, registryTokenMode bool) cache.Options {
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
	if locoNamespace != "" && registryTokenMode {
		registryPull, err := labels.NewRequirement(locov1alpha1.LabelRegistryPull, selection.Exists, nil)
		if err != nil {
			panic(fmt.Errorf("select registry pull secrets: %w", err))
		}
		registryPullSelector := labels.NewSelector().Add(*registryPull)
		secrets = cache.ByObject{
			Label: managedSelector,
			Namespaces: map[string]cache.Config{
				locoNamespace:       {LabelSelector: registryPullSelector},
				cache.AllNamespaces: {},
			},
		}
	}
	podSet := labels.Set{
		locolabels.LabelManagedBy: locolabels.ControllerManagedBy,
		managed.LabelComponent:    componentApplication,
	}
	podSelector := labels.SelectorFromSet(podSet)
	applicationPods := cache.ByObject{Label: podSelector}
	stripManagedFields := cache.TransformStripManagedFields()
	return cache.Options{
		DefaultTransform: stripManagedFields,
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Namespace{}:                      managedObjects,
			&corev1.Secret{}:                         secrets,
			&corev1.Service{}:                        managedObjects,
			&corev1.ServiceAccount{}:                 managedObjects,
			&appsv1.Deployment{}:                     managedObjects,
			&autoscalingv2.HorizontalPodAutoscaler{}: managedObjects,
			&v1Gateway.HTTPRoute{}:                   managedObjects,
			&corev1.Pod{}:                            applicationPods,
		},
	}
}

func applicationForObject(_ context.Context, obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	namespace, ok := annotations[locolabels.AnnotationApplicationNamespace]
	if !ok || namespace == "" {
		return nil
	}
	name, ok := annotations[locolabels.AnnotationApplicationName]
	if !ok || name == "" {
		return nil
	}
	key := types.NamespacedName{Namespace: namespace, Name: name}
	return []reconcile.Request{{NamespacedName: key}}
}

func (r *ApplicationReconciler) applicationPerWorkspace(ctx context.Context, _ client.Object) []reconcile.Request {
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
