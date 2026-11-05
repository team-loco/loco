package controller

import (
	"testing"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestMetricsDiscoveryAndIngressAreOptIn(t *testing.T) {
	app := telemetryTestApplication()
	r := &LocoResourceReconciler{
		Gateway:                  testGateway,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		Telemetry:                TelemetryConfig{ScrapeNamespace: "metrics-node", ScrapeCollector: "node-collector"},
	}
	for _, enabled := range []bool{true, false} {
		app.Spec.ServiceSpec.Obs = &locov1alpha1.ObsSpec{
			Metrics: locov1alpha1.MetricsSpec{Enabled: enabled, Port: 9090},
		}
		deployment := buildDeployment(t, app)
		template := deployment.Spec.Template
		if enabled {
			if template.Labels[locolabels.LabelMetricsPort] != "9090" ||
				template.Annotations[locolabels.AnnotationMetricsPath] != defaultMetricsPath {
				t.Fatalf("metadata = %+v", template)
			}
		} else if _, present := template.Labels[locolabels.LabelMetricsPort]; present {
			t.Fatal("disabled endpoint still discoverable")
		}
		policy := r.metricsIngressPolicy(app)
		if !enabled {
			if len(policy.Spec.Ingress) != 0 {
				t.Fatal("disabled endpoint still allows metrics ingress")
			}
			continue
		}
		if len(policy.Spec.Ingress) != 1 {
			t.Fatalf("ingress = %+v", policy.Spec)
		}
		rule := policy.Spec.Ingress[0]
		if rule.Ports[0].Port.IntVal != 9090 {
			t.Fatal("wrong metrics port")
		}
		peer := rule.From[0]
		if peer.NamespaceSelector.MatchLabels[labelNamespaceName] != "metrics-node" ||
			peer.PodSelector.MatchLabels[labelAppKubernetesName] != "node-collector" {
			t.Fatalf("peer = %+v", peer)
		}
	}
}
