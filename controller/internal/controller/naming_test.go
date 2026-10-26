package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testGateway                  = "test-gateway"
	testWorkspaceNamespacePrefix = "loco-ws-"
)

func testWorkspaceNamespace(app *locov1alpha1.Application) string {
	r := &LocoResourceReconciler{WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	return r.workspaceNamespace(app)
}

func ensureTestNamespace(ctx context.Context, kubeClient client.Client, app *locov1alpha1.Application) error {
	r := &LocoResourceReconciler{Client: kubeClient, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	return r.ensureNamespace(ctx, app)
}

func TestWorkspaceNamespaceUsesTheConfiguredPrefix(t *testing.T) {
	app := testApplication()
	r := &LocoResourceReconciler{WorkspaceNamespacePrefix: "tenant-"}
	want := "tenant-" + app.Spec.WorkspaceID
	if got := r.workspaceNamespace(app); got != want {
		t.Errorf("workspace namespace = %q, want %q", got, want)
	}
}

func TestNamesFitKubernetesLimitsForRealIDs(t *testing.T) {
	app := &locov1alpha1.Application{
		Spec: locov1alpha1.ApplicationSpec{
			Type:        testAppType,
			ResourceID:  "019a6f3e-8c2b-7d41-9e5f-3b2a1c0d9e8f",
			WorkspaceID: "019a6f3e-8c2b-7d41-9e5f-000000000003",
		},
	}

	namespace := testWorkspaceNamespace(app)
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		t.Errorf("namespace %q is not a valid namespace name: %v", namespace, errs)
	}
	if errs := validation.IsValidLabelValue(namespace); len(errs) > 0 {
		t.Errorf("namespace %q is not a valid label value: %v", namespace, errs)
	}

	name := getName(app)
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		t.Errorf("name %q is not a valid service name: %v", name, errs)
	}
	envSecretName := getEnvSecretName(app)
	roleName := getRoleName(app)
	bindingName := getRoleBindingName(app)
	routeName := getRouteName(app)
	for _, objectName := range []string{envSecretName, roleName, bindingName, routeName} {
		if errs := validation.IsDNS1123Subdomain(objectName); len(errs) > 0 {
			t.Errorf("object name %q is invalid: %v", objectName, errs)
		}
	}
}
