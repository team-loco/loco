package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	autoscalingv2ac "k8s.io/client-go/applyconfigurations/autoscaling/v2"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func (r *ApplicationReconciler) ensureAutoscaler(
	ctx context.Context,
	app *locov1alpha1.Application,
	deployment *appsv1ac.DeploymentApplyConfiguration,
) error {
	name := *deployment.Name
	namespace := *deployment.Namespace
	resources := app.Spec.ServiceSpec.Resources
	if !resources.Scalers.Enabled {
		hpa := &autoscalingv2.HorizontalPodAutoscaler{Name: name, Namespace: namespace}
		if err := r.Delete(ctx, hpa); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete autoscaler %s/%s: %w", namespace, name, err)
		}
		return nil
	}

	var metrics []*autoscalingv2ac.MetricSpecApplyConfiguration
	for _, metric := range []struct {
		name   corev1.ResourceName
		target int32
	}{
		{corev1.ResourceCPU, resources.Scalers.CPUTarget},
		{corev1.ResourceMemory, resources.Scalers.MemoryTarget},
	} {
		if metric.target == 0 {
			continue
		}
		target := autoscalingv2ac.MetricTarget().
			WithType(autoscalingv2.UtilizationMetricType).
			WithAverageUtilization(metric.target)
		resource := autoscalingv2ac.ResourceMetricSource().WithName(metric.name).WithTarget(target)
		metrics = append(
			metrics,
			autoscalingv2ac.MetricSpec().WithType(autoscalingv2.ResourceMetricSourceType).WithResource(resource),
		)
	}
	target := autoscalingv2ac.CrossVersionObjectReference().
		WithAPIVersion(appsv1.SchemeGroupVersion.String()).
		WithKind("Deployment").
		WithName(name)
	spec := autoscalingv2ac.HorizontalPodAutoscalerSpec().
		WithScaleTargetRef(target).
		WithMinReplicas(resources.Replicas.Min).
		WithMaxReplicas(resources.Replicas.Max).
		WithMetrics(metrics...)
	// Applications live in the control namespace, so the same-namespace Deployment owns the HPA.
	owner := metav1ac.OwnerReference().
		WithAPIVersion(appsv1.SchemeGroupVersion.String()).
		WithKind("Deployment").
		WithName(name).
		WithUID(*deployment.UID).
		WithController(true)
	hpa := autoscalingv2ac.HorizontalPodAutoscaler(name, namespace).
		WithLabels(managedLabels(app)).
		WithAnnotations(ownerAnnotations(app)).
		WithOwnerReferences(owner).
		WithSpec(spec)
	if err := r.Apply(ctx, hpa, managed.ApplyOptions()...); err != nil {
		return fmt.Errorf("apply autoscaler %s/%s: %w", namespace, name, err)
	}
	return nil
}
