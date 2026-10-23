package controller

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

	"github.com/team-loco/loco/controller/internal/managed"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testTelemetryNamespace  = "telemetry"
	testCollectorService    = "collector"
	testCollectorGRPCPort   = int32(14317)
	testCollectorHTTPPort   = int32(14318)
	testCollectorHost       = "collector.telemetry.svc.cluster.local"
	testCollectorEndpoint   = "http://collector.telemetry.svc.cluster.local:14318"
	testCollectorReference  = "$(LOCO_OTEL_HOST):$(LOCO_OTEL_GRPC_PORT)"
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
	app.Spec.EnvironmentName = testEnvironmentName
	app.Spec.DeploymentID = testDeploymentID
	app.Spec.OrganizationID = testOrganizationID
	return app
}

func buildDeployment(t *testing.T, app *locov1alpha1.Application) *appsv1ac.DeploymentApplyConfiguration {
	t.Helper()
	dep, err := desiredDeployment(app, envSecretMount{}, testTelemetry())
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
	requireEnv(t, dep, envOTelServiceName, getName(app))
	requireEnv(t, dep, envOTelResourceAttributes,
		"deployment.environment.name=production,loco.deployment.id="+testDeploymentID)
}

func TestDesiredDeploymentInjectsTheCollectorAddress(t *testing.T) {
	dep := buildDeployment(t, telemetryTestApplication())

	requireEnv(t, dep, envOTelHost, testCollectorHost)
	requireEnv(t, dep, envOTelGRPCPort, "14317")
	requireEnv(t, dep, envOTelHTTPPort, "14318")
}

func TestOTLPEndpointIsBuiltFromTheCollectorAddress(t *testing.T) {
	telemetry := testTelemetry()
	telemetry.CollectorService = "other"
	telemetry.Namespace = "elsewhere"
	telemetry.HTTPPort = 9999
	app := telemetryTestApplication()
	dep, err := desiredDeployment(app, envSecretMount{}, telemetry)
	if err != nil {
		t.Fatalf("desiredDeployment: %v", err)
	}

	host, _ := envValue(t, dep, envOTelHost)
	port, _ := envValue(t, dep, envOTelHTTPPort)
	requireEnv(t, dep, envOTLPEndpoint, "http://"+host+":"+port)
	requireEnv(t, dep, envOTLPEndpoint, "http://other.elsewhere.svc.cluster.local:9999")
}

func TestUserEnvDoesNotOverrideTheCollectorAddress(t *testing.T) {
	for _, name := range []string{envOTelHost, envOTelGRPCPort, envOTelHTTPPort} {
		app := telemetryTestApplication()
		app.Spec.ServiceSpec.Deployment.Env[name] = userEnvValue
		dep := buildDeployment(t, app)
		requireSingleEnv(t, dep, envOTelHost, testCollectorHost)
		requireSingleEnv(t, dep, envOTelGRPCPort, "14317")
		requireSingleEnv(t, dep, envOTelHTTPPort, "14318")
	}
}

func TestSystemEnvPrecedesUserEnvSoUserValuesCanReferenceIt(t *testing.T) {
	app := telemetryTestApplication()
	app.Spec.ServiceSpec.Deployment.Env["AAA_COLLECTOR"] = testCollectorReference
	app.Spec.ServiceSpec.Deployment.Env[envOTLPEndpoint] = "http://$(LOCO_OTEL_HOST):$(LOCO_OTEL_HTTP_PORT)"
	dep := buildDeployment(t, app)

	env := dep.Spec.Template.Spec.Containers[0].Env
	userEnv := app.Spec.ServiceSpec.Deployment.Env
	firstUser := slices.IndexFunc(env, func(envVar corev1ac.EnvVarApplyConfiguration) bool {
		_, user := userEnv[*envVar.Name]
		return user
	})
	if firstUser < 0 {
		t.Fatalf("no user env in %v", env)
	}
	for _, envVar := range env[firstUser:] {
		if _, user := userEnv[*envVar.Name]; !user {
			t.Errorf("system env %s follows user env, so user values cannot reference it", *envVar.Name)
		}
	}
	requireSingleEnv(t, dep, "AAA_COLLECTOR", testCollectorReference)
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

func TestTracingSamplingBelongsToTheApplicationSDK(t *testing.T) {
	for _, obs := range []*locov1alpha1.ObsSpec{nil, {}, {Logging: locov1alpha1.LoggingSpec{Enabled: true}}} {
		app := telemetryTestApplication()
		app.Spec.ServiceSpec.Obs = obs
		dep := buildDeployment(t, app)
		requireNoEnv(t, dep, envOTelTracesSampler)
		requireNoEnv(t, dep, envOTelTracesSamplerArg)
	}
}

func TestLegacyApplicationTracingDoesNotInjectSampling(t *testing.T) {
	app := telemetryTestApplication()
	legacy := []byte(`{"spec":{"serviceSpec":{"obs":{"tracing":{"enabled":true,"sampleRate":"0.25"}}}}}`)
	if err := json.Unmarshal(legacy, app); err != nil {
		t.Fatal(err)
	}
	dep := buildDeployment(t, app)
	requireNoEnv(t, dep, envOTelTracesSampler)
	requireNoEnv(t, dep, envOTelTracesSamplerArg)
	requireEnv(t, dep, envOTLPEndpoint, testCollectorEndpoint)
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
	name := getName(app)

	labels := dep.Spec.Template.Labels
	if got := labels[labelAppKubernetesName]; got != name {
		t.Errorf("pod label %s = %q, want %q", labelAppKubernetesName, got, name)
	}
	wantSelector := map[string]string{labelApp: name, labelEnvironmentID: app.Spec.EnvironmentID}
	if got := dep.Spec.Selector.MatchLabels; !maps.Equal(got, wantSelector) {
		t.Errorf("selector = %v, want %v", got, wantSelector)
	}
}

func TestPodTemplateCarriesTheOrganizationLabel(t *testing.T) {
	app := telemetryTestApplication()
	dep := buildDeployment(t, app)

	if got := dep.Spec.Template.Labels[managed.LabelOrganizationID]; got != testOrganizationID {
		t.Errorf("pod label %s = %q, want %q", managed.LabelOrganizationID, got, testOrganizationID)
	}
	if got := dep.Labels[managed.LabelOrganizationID]; got != testOrganizationID {
		t.Errorf("deployment label %s = %q, want %q", managed.LabelOrganizationID, got, testOrganizationID)
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
