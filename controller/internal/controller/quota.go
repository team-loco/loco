package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	workspaceQuotaName         = "workspace-quota"
	workspaceLimitRangeName    = "workspace-defaults"
	defaultWorkspaceCPU        = "8"
	defaultWorkspaceMemory     = "16Gi"
	defaultWorkspacePods       = "50"
	defaultContainerCPURequest = "100m"
	defaultContainerMemRequest = "128Mi"
	defaultContainerCPULimit   = "500m"
	defaultContainerMemLimit   = "512Mi"
)

type workspaceLimits struct {
	cpu    resource.Quantity
	memory resource.Quantity
	pods   resource.Quantity
}

func parseQuantityOrDefault(name, value, fallback string) (resource.Quantity, error) {
	if value == "" {
		value = fallback
	}
	quantity, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return quantity, nil
}

func parseWorkspaceLimits(cpu, memory, pods string) (workspaceLimits, error) {
	cpuQuantity, err := parseQuantityOrDefault("LOCO_WORKSPACE_CPU", cpu, defaultWorkspaceCPU)
	if err != nil {
		return workspaceLimits{}, err
	}
	memoryQuantity, err := parseQuantityOrDefault("LOCO_WORKSPACE_MEMORY", memory, defaultWorkspaceMemory)
	if err != nil {
		return workspaceLimits{}, err
	}
	podsQuantity, err := parseQuantityOrDefault("LOCO_WORKSPACE_PODS", pods, defaultWorkspacePods)
	if err != nil {
		return workspaceLimits{}, err
	}
	return workspaceLimits{cpu: cpuQuantity, memory: memoryQuantity, pods: podsQuantity}, nil
}

var defaultWorkspaceLimits = workspaceLimits{
	cpu:    resource.MustParse(defaultWorkspaceCPU),
	memory: resource.MustParse(defaultWorkspaceMemory),
	pods:   resource.MustParse(defaultWorkspacePods),
}

func (r *LocoResourceReconciler) effectiveWorkspaceLimits() workspaceLimits {
	if r.workspaceLimits.pods.IsZero() {
		return defaultWorkspaceLimits
	}
	return r.workspaceLimits
}

func workspaceResourceQuota(
	locoRes *locov1alpha1.Application,
	limits workspaceLimits,
) *corev1ac.ResourceQuotaApplyConfiguration {
	zero := resource.MustParse("0")
	hard := corev1.ResourceList{
		corev1.ResourceRequestsCPU:            limits.cpu,
		corev1.ResourceLimitsCPU:              limits.cpu,
		corev1.ResourceRequestsMemory:         limits.memory,
		corev1.ResourceLimitsMemory:           limits.memory,
		corev1.ResourcePods:                   limits.pods,
		corev1.ResourceServices:               limits.pods,
		corev1.ResourceServicesLoadBalancers:  zero,
		corev1.ResourceServicesNodePorts:      zero,
		corev1.ResourcePersistentVolumeClaims: zero,
	}
	spec := corev1ac.ResourceQuotaSpec().WithHard(hard)
	labels := workspacePolicyLabels(locoRes)
	return corev1ac.ResourceQuota(workspaceQuotaName, getNamespace(locoRes)).
		WithLabels(labels).
		WithSpec(spec)
}

func workspaceLimitRange(locoRes *locov1alpha1.Application) *corev1ac.LimitRangeApplyConfiguration {
	defaultRequest := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(defaultContainerCPURequest),
		corev1.ResourceMemory: resource.MustParse(defaultContainerMemRequest),
	}
	defaultLimit := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(defaultContainerCPULimit),
		corev1.ResourceMemory: resource.MustParse(defaultContainerMemLimit),
	}
	item := corev1ac.LimitRangeItem().
		WithType(corev1.LimitTypeContainer).
		WithDefaultRequest(defaultRequest).
		WithDefault(defaultLimit)
	spec := corev1ac.LimitRangeSpec().WithLimits(item)
	labels := workspacePolicyLabels(locoRes)
	return corev1ac.LimitRange(workspaceLimitRangeName, getNamespace(locoRes)).
		WithLabels(labels).
		WithSpec(spec)
}

func (r *LocoResourceReconciler) ensureWorkspaceQuota(ctx context.Context, locoRes *locov1alpha1.Application) error {
	opts := applyOptions()
	limitRange := workspaceLimitRange(locoRes)
	if err := r.Apply(ctx, limitRange, opts...); err != nil {
		return fmt.Errorf("apply limit range %s/%s: %w", *limitRange.Namespace, *limitRange.Name, err)
	}
	limits := r.effectiveWorkspaceLimits()
	quota := workspaceResourceQuota(locoRes, limits)
	if err := r.Apply(ctx, quota, opts...); err != nil {
		return fmt.Errorf("apply resource quota %s/%s: %w", *quota.Namespace, *quota.Name, err)
	}
	return nil
}
