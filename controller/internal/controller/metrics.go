package controller

import (
	"context"
	"errors"

	"github.com/team-loco/loco/controller/internal/isolation"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"
)

var errMetricsCollectorMissing = errors.New("metrics pulling requires the metrics collector namespace and service")

func (r *LocoResourceReconciler) metricsIngressPolicy(
	app *locov1alpha1.Application,
) *networkingv1ac.NetworkPolicyApplyConfiguration {
	selector := metav1ac.LabelSelector().WithMatchLabels(map[string]string{labelApp: getName(app)})
	spec := networkingv1ac.NetworkPolicySpec().WithPodSelector(selector).WithPolicyTypes(networkingv1.PolicyTypeIngress)
	if obs := app.Spec.ServiceSpec.Obs; obs != nil && obs.Metrics.Enabled {
		peer := namespacedPodPeer(
			r.Telemetry.ScrapeNamespace,
			map[string]string{labelAppKubernetesName: r.Telemetry.ScrapeCollector},
		)
		rule := networkingv1ac.NetworkPolicyIngressRule().WithFrom(peer).WithPorts(tcpPort(obs.Metrics.Port))
		spec.WithIngress(rule)
	}
	return networkingv1ac.NetworkPolicy(getMetricsPolicyName(app), r.workspaceNamespace(app)).
		WithLabels(managedLabels(app)).
		WithAnnotations(ownerAnnotations(app)).
		WithSpec(spec)
}

func (r *LocoResourceReconciler) ensureMetricsIngressPolicy(ctx context.Context, app *locov1alpha1.Application) error {
	if obs := app.Spec.ServiceSpec.Obs; obs != nil && obs.Metrics.Enabled {
		if r.Telemetry.ScrapeNamespace == "" || r.Telemetry.ScrapeCollector == "" {
			return errMetricsCollectorMissing
		}
	}
	return isolation.ApplyPolicies(
		ctx,
		r.Client,
		[]*networkingv1ac.NetworkPolicyApplyConfiguration{r.metricsIngressPolicy(app)},
	)
}

func getMetricsPolicyName(app *locov1alpha1.Application) string {
	return getName(app) + "-metrics"
}
