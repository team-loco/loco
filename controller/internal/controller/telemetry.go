package controller

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	envOTLPEndpoint                = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPProtocol                = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTelServiceName             = "OTEL_SERVICE_NAME"
	envOTelResourceAttributes      = "OTEL_RESOURCE_ATTRIBUTES"
	envOTelTracesSampler           = "OTEL_TRACES_SAMPLER"
	envOTelTracesSamplerArg        = "OTEL_TRACES_SAMPLER_ARG"
	otlpProtocolHTTP               = "http/protobuf"
	otlpHTTPEndpointFormat         = "http://%s.%s.svc.cluster.local:%d"
	samplerParentBasedAlwaysOff    = "parentbased_always_off"
	samplerParentBasedTraceIDRatio = "parentbased_traceidratio"
	attributeEnvironmentName       = "deployment.environment.name"
	attributeDeploymentID          = "loco.deployment.id"
	attributeSeparator             = ","
	attributeAssignment            = "="
)

var errTelemetryIncomplete = errors.New(
	"telemetry requires the observability namespace, the collector service and its OTLP gRPC and HTTP ports",
)

// TelemetryConfig locates the OpenTelemetry collector that app pods export to.
type TelemetryConfig struct {
	Namespace        string
	CollectorService string
	GRPCPort         int32
	HTTPPort         int32
}

// Validate reports whether every field needed to reach the collector is set.
func (c TelemetryConfig) Validate() error {
	if c.Namespace == "" || c.CollectorService == "" || c.GRPCPort == 0 || c.HTTPPort == 0 {
		return errTelemetryIncomplete
	}
	return nil
}

func (c TelemetryConfig) otlpHTTPEndpoint() string {
	return fmt.Sprintf(otlpHTTPEndpointFormat, c.CollectorService, c.Namespace, c.HTTPPort)
}

func openTelemetryEnvVars(locoRes *locov1alpha1.Application, telemetry TelemetryConfig) []corev1.EnvVar {
	endpoint := telemetry.otlpHTTPEndpoint()
	serviceName := getName(locoRes)
	connection := [][]corev1.EnvVar{
		{
			{Name: envOTLPEndpoint, Value: endpoint},
			{Name: envOTLPProtocol, Value: otlpProtocolHTTP},
		},
		{{Name: envOTelServiceName, Value: serviceName}},
		resourceAttributeVars(locoRes),
	}
	sampler := tracesSamplerSettings(locoRes.Spec.ServiceSpec.Obs)
	settings := slices.Concat(connection, sampler)

	userEnv := locoRes.Spec.ServiceSpec.Deployment.Env
	var vars []corev1.EnvVar
	for _, setting := range settings {
		if userSetsAny(userEnv, setting) {
			continue
		}
		vars = append(vars, setting...)
	}
	return vars
}

func userSetsAny(userEnv map[string]string, setting []corev1.EnvVar) bool {
	for _, envVar := range setting {
		if _, ok := userEnv[envVar.Name]; ok {
			return true
		}
	}
	return false
}

func resourceAttributeVars(locoRes *locov1alpha1.Application) []corev1.EnvVar {
	attributes := []struct{ key, value string }{
		{key: attributeEnvironmentName, value: locoRes.Spec.EnvironmentName},
		{key: attributeDeploymentID, value: locoRes.Spec.DeploymentID},
	}
	pairs := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		if attribute.value == "" {
			continue
		}
		escaped := url.PathEscape(attribute.value)
		pairs = append(pairs, attribute.key+attributeAssignment+escaped)
	}
	if len(pairs) == 0 {
		return nil
	}
	value := strings.Join(pairs, attributeSeparator)
	return []corev1.EnvVar{{Name: envOTelResourceAttributes, Value: value}}
}

func tracesSamplerSettings(obs *locov1alpha1.ObsSpec) [][]corev1.EnvVar {
	if obs == nil || !obs.Tracing.Enabled {
		return [][]corev1.EnvVar{{{Name: envOTelTracesSampler, Value: samplerParentBasedAlwaysOff}}}
	}
	return [][]corev1.EnvVar{
		{{Name: envOTelTracesSampler, Value: samplerParentBasedTraceIDRatio}},
		{{Name: envOTelTracesSamplerArg, Value: obs.Tracing.SampleRate}},
	}
}
