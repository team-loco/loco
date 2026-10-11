package controller

import (
	"errors"
	"maps"
	"strings"
	"testing"

	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testTelemetryNamespace    = "telemetry"
	testCollectorService      = "collector"
	testCollectorGRPCPort     = int32(14317)
	testCollectorHTTPPort     = int32(14318)
	testCollectorEndpoint     = "http://collector.telemetry.svc.cluster.local:14318"
	testEnvironmentName       = "production"
	testDeploymentID          = "019a6f3e-8c2b-7d41-9e5f-3b2a1c0d9e8f"
	testSampleRate            = "0.25"
	wantSamplerWithoutTracing = "parentbased_always_off"
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
	app.Spec.EnvironmentName = testEnvironmentName
	app.Spec.DeploymentID = testDeploymentID
	return app
}

func tracingEnabled() *locov1alpha1.ObsSpec {
	tracing := locov1alpha1.TracingSpec{Enabled: true, SampleRate: testSampleRate}
	return &locov1alpha1.ObsSpec{Tracing: tracing}
}

func buildDeployment(t *testing.T, app *locov1alpha1.Application) *appsv1ac.DeploymentApplyConfiguration {
	t.Helper()
	dep, err := desiredDeployment(app, "1", testTelemetry())
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
	requireEnv(t, dep, envOTelServiceName, getName(app))
	requireEnv(t, dep, envOTelResourceAttributes,
		"deployment.environment.name=production,loco.deployment.id="+testDeploymentID)
}

func TestResourceAttributesCarryNoTenantIdentity(t *testing.T) {
	app := telemetryTestApplication()
	dep := buildDeployment(t, app)

	attributes, ok := envValue(t, dep, envOTelResourceAttributes)
	if !ok {
		t.Fatalf("%s missing", envOTelResourceAttributes)
	}
	for _, tenantKey := range []string{"loco.workspace.id", "loco.environment.id", "loco.resource.id"} {
		if strings.Contains(attributes, tenantKey) {
			t.Errorf("%s sets %s, which the collector derives from pod labels", envOTelResourceAttributes, tenantKey)
		}
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

func TestTracesSamplerFollowsTheObsSpec(t *testing.T) {
	cases := []struct {
		name       string
		obs        *locov1alpha1.ObsSpec
		sampler    string
		samplerArg string
	}{
		{name: "no obs spec", obs: nil, sampler: wantSamplerWithoutTracing},
		{name: "no tracing spec", obs: &locov1alpha1.ObsSpec{}, sampler: wantSamplerWithoutTracing},
		{
			name:    "tracing disabled",
			obs:     &locov1alpha1.ObsSpec{Tracing: locov1alpha1.TracingSpec{SampleRate: "0.5"}},
			sampler: wantSamplerWithoutTracing,
		},
		{
			name:       "tracing enabled",
			obs:        tracingEnabled(),
			sampler:    "parentbased_traceidratio",
			samplerArg: testSampleRate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := telemetryTestApplication()
			app.Spec.ServiceSpec.Obs = tc.obs
			dep := buildDeployment(t, app)

			requireEnv(t, dep, envOTelTracesSampler, tc.sampler)
			if tc.samplerArg == "" {
				requireNoEnv(t, dep, envOTelTracesSamplerArg)
				return
			}
			requireEnv(t, dep, envOTelTracesSamplerArg, tc.samplerArg)
		})
	}
}

func TestUserEnvOverridesInjectedOpenTelemetryEnv(t *testing.T) {
	enabled := tracingEnabled()
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
			kept:     map[string]string{envOTelTracesSampler: samplerParentBasedTraceIDRatio},
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
			name:     "sampler argument keeps the sampler",
			userEnv:  envOTelTracesSamplerArg,
			replaced: []string{envOTelTracesSamplerArg},
			kept:     map[string]string{envOTelTracesSampler: samplerParentBasedTraceIDRatio},
		},
		{
			name:     "sampler keeps the sampler argument",
			userEnv:  envOTelTracesSampler,
			replaced: []string{envOTelTracesSampler},
			kept:     map[string]string{envOTelTracesSamplerArg: testSampleRate},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := telemetryTestApplication()
			app.Spec.ServiceSpec.Obs = enabled
			app.Spec.ServiceSpec.Deployment.Env[tc.userEnv] = "user"
			dep := buildDeployment(t, app)

			for _, name := range tc.replaced {
				requireNoEnv(t, dep, name)
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
	name := getName(app)

	labels := dep.Spec.Template.Labels
	if got := labels[labelAppKubernetesName]; got != name {
		t.Errorf("pod label %s = %q, want %q", labelAppKubernetesName, got, name)
	}
	wantSelector := map[string]string{labelApp: name}
	if got := dep.Spec.Selector.MatchLabels; !maps.Equal(got, wantSelector) {
		t.Errorf("selector = %v, want %v", got, wantSelector)
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
