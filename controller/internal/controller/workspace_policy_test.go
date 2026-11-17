package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/team-loco/loco/controller/internal/isolation"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

func TestWorkspacePolicyAllowsOtherEnvironmentsButNotOtherWorkspaces(t *testing.T) {
	const otherEnvironment = "another-environment"
	app := testApplication()
	reconciler := &ApplicationReconciler{
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		Telemetry:                testTelemetry(),
	}
	policies := reconciler.workspaceNetworkPolicies(app, isolation.EgressExclusions{})
	for _, policy := range policies {
		if *policy.Name != policyWorkspaceAccess {
			continue
		}
		selectors := []map[string]string{
			policy.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels,
			policy.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels,
		}
		for _, selection := range selectors {
			selector := labels.SelectorFromSet(selection)
			for _, tc := range []struct {
				workspace, environment string
				managed                bool
				want                   bool
			}{
				{app.Spec.WorkspaceID, app.Spec.EnvironmentID, true, true},
				{app.Spec.WorkspaceID, otherEnvironment, true, true},
				{"another-workspace", otherEnvironment, true, false},
				{app.Spec.WorkspaceID, otherEnvironment, false, false},
			} {
				namespace := labels.Set{
					locolabels.LabelWorkspaceID:   tc.workspace,
					locolabels.LabelEnvironmentID: tc.environment,
				}
				if tc.managed {
					namespace[locolabels.LabelManagedBy] = locolabels.ControllerManagedBy
					namespace[locolabels.LabelLocoApp] = namespaceAppLabelValue
				}
				if got := selector.Matches(namespace); got != tc.want {
					t.Errorf("namespace %v matched=%v want %v", namespace, got, tc.want)
				}
			}
		}
		return
	}
	t.Fatal("workspace access policy missing")
}
