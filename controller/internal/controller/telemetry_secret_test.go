package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const testSecretTelemetryValue = "synthetic-secret-otel-value"

func telemetrySecretFixture(
	t *testing.T, app *locov1alpha1.Application, name string,
) (*LocoResourceReconciler, *corev1.Secret) {
	t.Helper()
	stage := &corev1.Secret{
		Name: "env-telemetry", Namespace: app.Namespace,
		Labels: map[string]string{locov1alpha1.LabelPlacementRevision: "1"},
		Data:   map[string][]byte{name: []byte(testSecretTelemetryValue)},
	}
	app.Spec.ServiceSpec.Deployment.EnvSecretRef = &locov1alpha1.EnvSecretRef{Name: stage.Name, Revision: 1}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	funcs := interceptor.Funcs{
		Apply: func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
			return nil
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stage).WithInterceptorFuncs(funcs).Build()
	return &LocoResourceReconciler{
		Client:                   kube,
		APIReader:                kube,
		Gateway:                  testGateway,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}, stage
}

func mountTelemetryDeployment(
	t *testing.T, r *LocoResourceReconciler, app *locov1alpha1.Application,
) *appsv1ac.DeploymentApplyConfiguration {
	t.Helper()
	mount, err := r.ensureEnvSecret(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := desiredDeployment(app, testWorkspaceNamespace(app), mount, testTelemetry())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(dep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), testSecretTelemetryValue) {
		t.Fatal("secret value appeared in the Deployment")
	}
	return dep
}

func TestMountedSecretsOverrideOpenTelemetryDefaults(t *testing.T) {
	for _, name := range []string{
		envOTLPEndpoint, envOTLPProtocol, envOTelServiceName, envOTelResourceAttributes,
		envOTelTracesSampler, envOTelTracesSamplerArg,
	} {
		t.Run(name, func(t *testing.T) {
			app := telemetryTestApplication()
			r, _ := telemetrySecretFixture(t, app, name)
			dep := mountTelemetryDeployment(t, r, app)
			requireNoEnv(t, dep, name)
			if name == envOTLPEndpoint || name == envOTLPProtocol {
				requireNoEnv(t, dep, envOTLPEndpoint)
				requireNoEnv(t, dep, envOTLPProtocol)
			}
			if name == envOTelTracesSamplerArg || name == envOTelTracesSampler {
				requireNoEnv(t, dep, envOTelTracesSampler)
				requireNoEnv(t, dep, envOTelTracesSamplerArg)
			}
		})
	}
}

func TestMountedTelemetrySecretsKeepPlainAndLocoPrecedence(t *testing.T) {
	for _, name := range []string{envOTelServiceName, envOTelTracesSampler, envOTelTracesSamplerArg} {
		app := telemetryTestApplication()
		app.Spec.ServiceSpec.Deployment.Env[name] = userEnvValue
		r, _ := telemetrySecretFixture(t, app, name)
		dep := mountTelemetryDeployment(t, r, app)
		requireSingleEnv(t, dep, name, userEnvValue)
	}
	app := telemetryTestApplication()
	r, _ := telemetrySecretFixture(t, app, "LOCO_DEPLOYMENT_ID")
	dep := mountTelemetryDeployment(t, r, app)
	requireSingleEnv(t, dep, "LOCO_DEPLOYMENT_ID", testDeploymentID)
}

func TestRemovingMountedSamplerSecretLeavesSDKDefault(t *testing.T) {
	app := telemetryTestApplication()
	r, stage := telemetrySecretFixture(t, app, envOTelTracesSamplerArg)
	dep := mountTelemetryDeployment(t, r, app)
	requireNoEnv(t, dep, envOTelTracesSamplerArg)

	stage.Data = map[string][]byte{}
	stage.Labels[locov1alpha1.LabelPlacementRevision] = "2"
	if err := r.Update(t.Context(), stage); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ensureEnvSecret(t.Context(), app); !errors.Is(err, errEnvSecretPending) {
		t.Fatalf("ahead revision = %v, want pending", err)
	}
	app.Spec.ServiceSpec.Deployment.EnvSecretRef.Revision = 2
	dep = mountTelemetryDeployment(t, r, app)
	requireNoEnv(t, dep, envOTelTracesSamplerArg)
	requireNoEnv(t, dep, envOTelTracesSampler)

	app.Spec.ServiceSpec.Deployment.EnvSecretRef = nil
	dep = mountTelemetryDeployment(t, r, app)
	requireNoEnv(t, dep, envOTelTracesSamplerArg)
	requireNoEnv(t, dep, envOTelTracesSampler)
}
