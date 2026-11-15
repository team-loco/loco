package controller

import (
	"errors"
	"maps"
	"slices"
	"testing"

	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testTelemetryNamespace  = "telemetry"
	testCollectorService    = "collector"
	testCollectorGRPCPort   = int32(14317)
	testCollectorHTTPPort   = int32(14318)
	testCollectorEndpoint   = "http://collector.telemetry.svc.cluster.local:14318"
	testEnvironmentName     = "production"
	testDeploymentID        = "019a6f3e-8c2b-7d41-9e5f-3b2a1c0d9e8f"
	testOrganizationID      = "019a6f3e-8c2b-7d41-9e5f-3b2a1c0d9e90"
	testSampleRate          = "0.25"
	envOTelTracesSampler    = "OTEL_TRACES_SAMPLER"
	envOTelTracesSamplerArg = "OTEL_TRACES_SAMPLER_ARG"
	userEnvValue            = "user"
)

func testTelemetry() TelemetryConfig {
	return TelemetryConfig{
		Namespace:        testTelemetryNamespace,
		CollectorService: testCollectorService,
		GRPCPort:         testCollectorGRPCPort,
		HTTPPort:         testCollectorHTTPPort,
	}
}

func telemetryTestApplication() *locov1alpha1.Application {
	app := testApplication()
	app.Spec.ServiceSpec.Deployment.AutoinjectOtelEnvVars = true
	app.Spec.EnvironmentName = testEnvironmentName
	app.Spec.DeploymentID = testDeploymentID
	app.Spec.OrganizationID = testOrganizationID
	return app
}

func buildDeployment(t *testing.T, app *locov1alpha1.Application) *appsv1ac.DeploymentApplyConfiguration {
	t.Helper()
	dep, err := desiredDeployment(
		app,
		testWorkspaceNamespace(app),
		envSecretMount{},
		testTelemetry(),
		testMinReadySeconds,
	)
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}
	return dep
}

func requireEnv(t *testing.T, dep *appsv1ac.DeploymentApplyConfiguration, name, want string) {
	t.Helper()
	got, ok := envValue(t, dep, name)
	if !ok {
		t.Fatalf("%s missing", name)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

func requireSingleEnv(t *testing.T, dep *appsv1ac.DeploymentApplyConfiguration, name, want string) {
	t.Helper()
	var values []string
	for _, envVar := range dep.Spec.Template.Spec.Containers[0].Env {
		if *envVar.Name == name {
			values = append(values, *envVar.Value)
		}
	}
	if !slices.Equal(values, []string{want}) {
		t.Errorf("%s = %q, want only %q", name, values, want)
	}
}

func requireNoEnv(t *testing.T, dep *appsv1ac.DeploymentApplyConfiguration, name string) {
	t.Helper()
	if got, ok := envValue(t, dep, name); ok {
		t.Errorf("%s = %q, want it unset", name, got)
	}
}

func TestDesiredDeploymentPointsOpenTelemetryAtTheCollector(t *testing.T) {
	app := telemetryTestApplication()
	dep := buildDeployment(t, app)

	requireEnv(t, dep, envOTLPEndpoint, testCollectorEndpoint)
	requireEnv(t, dep, envOTLPProtocol, otlpProtocolHTTP)
	requireEnv(t, dep, envOTelServiceName, app.Spec.Slug)
	requireEnv(t, dep, envOTelResourceAttributes,
		"deployment.environment.name=production,loco.deployment.id="+testDeploymentID)
}

func TestDesiredDeploymentLeavesSDKConfigurationToTheApplicationByDefault(t *testing.T) {
	app := testApplication()
	app.Spec.ServiceSpec.Deployment.Env[envOTLPEndpoint] = "https://telemetry.example.test"
	app.Spec.ServiceSpec.Deployment.Env[envOTelServiceName] = "my-service"
	dep := buildDeployment(t, app)
	requireSingleEnv(t, dep, envOTLPEndpoint, "https://telemetry.example.test")
	requireSingleEnv(t, dep, envOTelServiceName, "my-service")
	requireNoEnv(t, dep, envOTLPProtocol)
	requireNoEnv(t, dep, envOTelResourceAttributes)
}

func TestOTLPEndpointIsBuiltFromTheCollectorAddress(t *testing.T) {
	telemetry := testTelemetry()
	telemetry.CollectorService = "other"
	telemetry.Namespace = "elsewhere"
	telemetry.HTTPPort = 9999
	app := telemetryTestApplication()
	dep, err := desiredDeployment(app, testWorkspaceNamespace(app), envSecretMount{}, telemetry, testMinReadySeconds)
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}

	requireEnv(t, dep, envOTLPEndpoint, "http://other.elsewhere.svc.cluster.local:9999")
}

}

func TestResourceAttributesArePercentEncoded(t *testing.T) {
	app := telemetryTestApplication()
	app.Spec.EnvironmentName = "staging, east=2"
	dep := buildDeployment(t, app)

	requireEnv(t, dep, envOTelResourceAttributes,
		"deployment.environment.name=staging%2C%20east=2,loco.deployment.id="+testDeploymentID)
}

func TestResourceAttributesSkipEmptyValues(t *testing.T) {
	app := telemetryTestApplication()
	app.Spec.EnvironmentName = ""
	dep := buildDeployment(t, app)
	requireEnv(t, dep, envOTelResourceAttributes, "loco.deployment.id="+testDeploymentID)

	app.Spec.DeploymentID = ""
	dep = buildDeployment(t, app)
	requireNoEnv(t, dep, envOTelResourceAttributes)
}

func TestUserTracingSamplerAndArgumentArePreserved(t *testing.T) {
	app := telemetryTestApplication()
	app.Spec.ServiceSpec.Deployment.Env[envOTelTracesSampler] = "parentbased_traceidratio"
	app.Spec.ServiceSpec.Deployment.Env[envOTelTracesSamplerArg] = testSampleRate
	dep := buildDeployment(t, app)
	requireSingleEnv(t, dep, envOTelTracesSampler, "parentbased_traceidratio")
	requireSingleEnv(t, dep, envOTelTracesSamplerArg, testSampleRate)
	requireEnv(t, dep, envOTLPEndpoint, testCollectorEndpoint)
}

func TestUserEnvOverridesInjectedOpenTelemetryEnv(t *testing.T) {
	cases := []struct {
		name     string
		userEnv  string
		replaced []string
		kept     map[string]string
	}{
		{
			name:     "service name",
			userEnv:  envOTelServiceName,
			replaced: []string{envOTelServiceName},
			kept:     map[string]string{envOTLPEndpoint: testCollectorEndpoint},
		},
		{
			name:     "endpoint replaces the protocol with it",
			userEnv:  envOTLPEndpoint,
			replaced: []string{envOTLPEndpoint, envOTLPProtocol},
		},
		{
			name:     "protocol replaces the endpoint with it",
			userEnv:  envOTLPProtocol,
			replaced: []string{envOTLPEndpoint, envOTLPProtocol},
		},
		{
			name:     "resource attributes",
			userEnv:  envOTelResourceAttributes,
			replaced: []string{envOTelResourceAttributes},
		},
		{
			name:     "sampler argument does not inject a sampler",
			userEnv:  envOTelTracesSamplerArg,
			replaced: []string{envOTelTracesSamplerArg, envOTelTracesSampler},
		},
		{
			name:     "sampler does not inject an argument",
			userEnv:  envOTelTracesSampler,
			replaced: []string{envOTelTracesSampler, envOTelTracesSamplerArg},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := telemetryTestApplication()
			app.Spec.ServiceSpec.Deployment.Env[tc.userEnv] = userEnvValue
			dep := buildDeployment(t, app)

			requireSingleEnv(t, dep, tc.userEnv, userEnvValue)
			for _, name := range tc.replaced {
				if name != tc.userEnv {
					requireNoEnv(t, dep, name)
				}
			}
			for name, want := range tc.kept {
				requireEnv(t, dep, name, want)
			}
		})
	}
}

func TestUserEnvDoesNotOverrideLocoEnv(t *testing.T) {
	app := telemetryTestApplication()
	app.Spec.ServiceSpec.Deployment.Env["LOCO_DEPLOYMENT_ID"] = "user"
	dep := buildDeployment(t, app)
	requireEnv(t, dep, "LOCO_DEPLOYMENT_ID", testDeploymentID)
}

func TestPodTemplateCarriesTheAppNameLabelOutsideTheSelector(t *testing.T) {
	app := telemetryTestApplication()
	dep := buildDeployment(t, app)
	name := testAppName(app)

	labels := dep.Spec.Template.Labels
	if got := labels[labelAppKubernetesName]; got != name {
		t.Errorf("pod label %s = %q, want %q", labelAppKubernetesName, got, name)
	}
	wantSelector := map[string]string{labelApp: name}
	if got := dep.Spec.Selector.MatchLabels; !maps.Equal(got, wantSelector) {
		t.Errorf("selector = %v, want %v", got, wantSelector)
	}
}

func TestPodTemplateCarriesTheOrganizationLabel(t *testing.T) {
	app := telemetryTestApplication()
	dep := buildDeployment(t, app)

	if got := dep.Spec.Template.Labels[locolabels.LabelOrganizationID]; got != testOrganizationID {
		t.Errorf("pod label %s = %q, want %q", locolabels.LabelOrganizationID, got, testOrganizationID)
	}
	if got := dep.Labels[locolabels.LabelOrganizationID]; got != testOrganizationID {
		t.Errorf("deployment label %s = %q, want %q", locolabels.LabelOrganizationID, got, testOrganizationID)
	}
}

func TestTelemetryConfigRequiresEveryField(t *testing.T) {
	if err := testTelemetry().Validate(); err != nil {
		t.Fatalf("complete telemetry config rejected: %v", err)
	}
	missing := map[string]func(*TelemetryConfig){
		"namespace":         func(c *TelemetryConfig) { c.Namespace = "" },
		"collector service": func(c *TelemetryConfig) { c.CollectorService = "" },
		"grpc port":         func(c *TelemetryConfig) { c.GRPCPort = 0 },
		"http port":         func(c *TelemetryConfig) { c.HTTPPort = 0 },
	}
	for name, change := range missing {
		t.Run(name, func(t *testing.T) {
			config := testTelemetry()
			change(&config)
			if err := config.Validate(); !errors.Is(err, errTelemetryIncomplete) {
				t.Errorf("Validate() = %v, want errTelemetryIncomplete", err)
			}
		})
	}
}

func TestUserResourceServiceNameIsNotOverridden(t *testing.T) {
	for _, attributes := range []string{"service.name=custom", "deployment.environment.name=test, service.name=custom"} {
		app := telemetryTestApplication()
		app.Spec.ServiceSpec.Deployment.Env[envOTelResourceAttributes] = attributes
		dep := buildDeployment(t, app)
		requireNoEnv(t, dep, envOTelServiceName)
		requireSingleEnv(t, dep, envOTelResourceAttributes, attributes)
	}
}
