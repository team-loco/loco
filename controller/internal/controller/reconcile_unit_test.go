package controller

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testAppType   = "SERVICE"
	testNamespace = "default"
)

func testApplication() *locov1alpha1.Application {
	return &locov1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "loco"},
		Spec: locov1alpha1.ApplicationSpec{
			Type:        testAppType,
			ResourceID:  "1",
			WorkspaceID: "2",
			ServiceSpec: &locov1alpha1.ServiceSpec{
				Deployment: &locov1alpha1.ServiceDeploymentSpec{
					Image: "registry.example.com/app:v1",
					Port:  8080,
					Env: map[string]string{
						"ZETA":  "1",
						"ALPHA": "2",
						"MID":   "3",
						"BETA":  "4",
					},
				},
				Resources: &locov1alpha1.ResourcesSpec{
					CPU:      "250m",
					Memory:   "256Mi",
					Replicas: locov1alpha1.ReplicasSpec{Min: 2, Max: 3},
				},
				Routing: &locov1alpha1.RoutingSpec{HostName: "app.example.com"},
			},
		},
	}
}

func envValue(t *testing.T, dep *appsv1ac.DeploymentApplyConfiguration, name string) (string, bool) {
	t.Helper()
	containers := dep.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected one container, got %d", len(containers))
	}
	for _, env := range containers[0].Env {
		if ptr.Deref(env.Name, "") == name {
			return ptr.Deref(env.Value, ""), true
		}
	}
	return "", false
}

func TestDesiredDeploymentIsDeterministic(t *testing.T) {
	app := testApplication()
	first, err := desiredDeployment(app, "7")
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for range 20 {
		next, err := desiredDeployment(app, "7")
		if err != nil {
			t.Fatalf("desiredDeployment: %v", err)
		}
		nextJSON, err := json.Marshal(next)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(nextJSON) != string(firstJSON) {
			t.Fatalf("deployment differs between builds:\n%s\n%s", firstJSON, nextJSON)
		}
	}
}

func TestDesiredDeploymentSourcesUserEnvFromSecret(t *testing.T) {
	app := testApplication()
	dep, err := desiredDeployment(app, "7")
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}

	for name := range app.Spec.ServiceSpec.Deployment.Env {
		if _, ok := envValue(t, dep, name); ok {
			t.Errorf("user env %s is inlined in the pod spec", name)
		}
	}

	container := dep.Spec.Template.Spec.Containers[0]
	if len(container.EnvFrom) != 1 || container.EnvFrom[0].SecretRef == nil {
		t.Fatalf("expected a single secret envFrom, got %+v", container.EnvFrom)
	}
	secretName := ptr.Deref(container.EnvFrom[0].SecretRef.Name, "")
	wantSecretName := getEnvSecretName(app)
	if secretName != wantSecretName {
		t.Errorf("envFrom secret = %q, want %q", secretName, wantSecretName)
	}

	version := dep.Spec.Template.Annotations[annotationEnvSecretRV]
	if version != "7" {
		t.Errorf("env secret version annotation = %q, want 7", version)
	}
	if got := ptr.Deref(dep.Spec.Replicas, 0); got != 2 {
		t.Errorf("replicas = %d, want 2", got)
	}
}

func TestDesiredDeploymentWithoutRouting(t *testing.T) {
	app := testApplication()
	app.Spec.ServiceSpec.Routing = nil
	dep, err := desiredDeployment(app, "1")
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}
	value, ok := envValue(t, dep, "LOCO_PUBLIC_DOMAIN")
	if !ok {
		t.Fatal("LOCO_PUBLIC_DOMAIN missing")
	}
	if value != "" {
		t.Errorf("LOCO_PUBLIC_DOMAIN = %q, want empty", value)
	}
}

func TestDesiredDeploymentDefaultsWithoutResources(t *testing.T) {
	app := testApplication()
	app.Spec.ServiceSpec.Resources = nil
	dep, err := desiredDeployment(app, "1")
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}
	if got := ptr.Deref(dep.Spec.Replicas, 0); got != 1 {
		t.Errorf("replicas = %d, want 1", got)
	}
	limits := *dep.Spec.Template.Spec.Containers[0].Resources.Limits
	cpuLimit := limits[corev1.ResourceCPU]
	gotCPULimit := cpuLimit.String()
	if gotCPULimit != defaultCPULimit {
		t.Errorf("cpu limit = %s, want %s", gotCPULimit, defaultCPULimit)
	}
}

func TestDesiredDeploymentRejectsInvalidQuantity(t *testing.T) {
	app := testApplication()
	app.Spec.ServiceSpec.Resources.CPU = "lots"
	if _, err := desiredDeployment(app, "1"); err == nil {
		t.Fatal("expected an error for an unparsable cpu quantity")
	}
}

func TestDeploymentReady(t *testing.T) {
	build := func(generation, observed int64, updated, available int32) *appsv1ac.DeploymentApplyConfiguration {
		status := appsv1ac.DeploymentStatus().
			WithObservedGeneration(observed).
			WithUpdatedReplicas(updated).
			WithAvailableReplicas(available)
		return appsv1ac.Deployment("d", "ns").WithGeneration(generation).WithStatus(status)
	}

	noStatus := appsv1ac.Deployment("d", "ns").WithGeneration(1)
	rolledOut := build(3, 3, 2, 2)
	unobserved := build(4, 3, 2, 2)
	inProgress := build(3, 3, 1, 2)
	unavailable := build(3, 3, 2, 1)
	cases := []struct {
		name string
		dep  *appsv1ac.DeploymentApplyConfiguration
		want bool
	}{
		{name: "rolled out", dep: rolledOut, want: true},
		{name: "generation not observed", dep: unobserved, want: false},
		{name: "rollout in progress", dep: inProgress, want: false},
		{name: "not available", dep: unavailable, want: false},
		{name: "no status", dep: noStatus, want: false},
		{name: "nil", dep: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deploymentReady(tc.dep, 2); got != tc.want {
				t.Errorf("deploymentReady = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnsureHTTPRouteDeletesRouteWhenRoutingRemoved(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1Gateway.Install(scheme); err != nil {
		t.Fatalf("install gateway scheme: %v", err)
	}

	app := testApplication()
	app.Spec.ServiceSpec.Routing = nil
	namespace := getNamespace(app)
	routeName := getName(app) + "-route"
	existing := &v1Gateway.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: namespace}}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	r := &LocoResourceReconciler{Client: kubeClient}

	testCtx := context.Background()
	if err := r.ensureHTTPRoute(testCtx, app); err != nil {
		t.Fatalf("ensureHTTPRoute: %v", err)
	}
	key := client.ObjectKey{Namespace: namespace, Name: routeName}
	err := kubeClient.Get(testCtx, key, &v1Gateway.HTTPRoute{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected route to be deleted, got %v", err)
	}

	if err := r.ensureHTTPRoute(testCtx, app); err != nil {
		t.Fatalf("ensureHTTPRoute without an existing route: %v", err)
	}
}

func TestSetPhaseOnlyTouchesStatusOnChange(t *testing.T) {
	app := testApplication()
	setPhase(app, phaseDeploying, "waiting")
	updatedAt := app.Status.UpdatedAt
	if updatedAt == nil || app.Status.StartedAt == nil {
		t.Fatal("expected timestamps to be set on the first transition")
	}

	original := app.DeepCopy()
	setPhase(app, phaseDeploying, "waiting")
	if app.Status.UpdatedAt != updatedAt || app.Status.Phase != original.Status.Phase {
		t.Fatal("status changed although phase and message did not")
	}

	setPhase(app, phaseReady, "ready")
	if app.Status.CompletedAt == nil {
		t.Fatal("expected completedAt on the transition to ready")
	}
}

func TestApplicationForObject(t *testing.T) {
	app := testApplication()
	annotations := ownerAnnotations(app)
	obj := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: annotations}}
	testCtx := context.Background()

	requests := applicationForObject(testCtx, obj)
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	if requests[0].Namespace != app.Namespace || requests[0].Name != app.Name {
		t.Errorf("request = %v, want %s/%s", requests[0].NamespacedName, app.Namespace, app.Name)
	}

	unowned := &corev1.Secret{}
	if got := applicationForObject(testCtx, unowned); len(got) != 0 {
		t.Errorf("expected no requests for an unannotated object, got %v", got)
	}
}
