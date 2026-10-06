package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/validation"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestNamesFitKubernetesLimitsForRealIDs(t *testing.T) {
	app := &locov1alpha1.Application{
		Spec: locov1alpha1.ApplicationSpec{
			Type:        testAppType,
			ResourceID:  "019a6f3e-8c2b-7d41-9e5f-3b2a1c0d9e8f",
			WorkspaceID: "019a6f3e-8c2b-7d41-9e5f-000000000003",
		},
	}

	namespace := getNamespace(app)
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
	imageSecretName := getImageSecretName(app)
	envSecretName := getEnvSecretName(app)
	roleName := getRoleName(app)
	bindingName := getRoleBindingName(app)
	routeName := getRouteName(app)
	for _, objectName := range []string{imageSecretName, envSecretName, roleName, bindingName, routeName} {
		if errs := validation.IsDNS1123Subdomain(objectName); len(errs) > 0 {
			t.Errorf("object name %q is invalid: %v", objectName, errs)
		}
	}
}
