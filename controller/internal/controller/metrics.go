package controller

import (
	"context"
	"strconv"

	networkingv1 "k8s.io/api/networking/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1ac "k8s.io/client-go/applyconfigurations/networking/v1"

	"github.com/team-loco/loco/controller/internal/isolation"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// addMetricsScrape marks the pod for the metrics collector, which discovers targets by the
// port label and reads the scrape path from the annotation.
func addMetricsScrape(obs *locov1alpha1.ObsSpec, podLabels, podAnnotations map[string]string) {
	if obs == nil || !obs.Metrics.Enabled {
		return
	}
	podLabels[locolabels.LabelMetricsPort] = strconv.FormatInt(int64(obs.Metrics.Port), 10)
	podAnnotations[locolabels.AnnotationMetricsPath] = obs.Metrics.Path
}

func (r *LocoResourceReconciler) metricsIngressPolicy(
	app *locov1alpha1.Application,
) *networkingv1ac.NetworkPolicyApplyConfiguration {
	selector := metav1ac.LabelSelector().
		WithMatchLabels(map[string]string{labelApp: locov1alpha1.ApplicationName(app.Spec.ResourceID)})
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
	return isolation.ApplyPolicies(
		ctx,
		r.Client,
		[]*networkingv1ac.NetworkPolicyApplyConfiguration{r.metricsIngressPolicy(app)},
	)
}

func getMetricsPolicyName(app *locov1alpha1.Application) string {
	return locov1alpha1.ApplicationName(app.Spec.ResourceID) + "-metrics"
}
