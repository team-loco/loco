package controller

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	defaultMetricsPath        = "/metrics"
	labelMetricsPort          = "loco.io/metrics-port"
	annotationMetricsPath     = "loco.io/metrics-path"
	envOTLPEndpoint           = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPProtocol           = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTelServiceName        = "OTEL_SERVICE_NAME"
	envOTelResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
	otlpProtocolHTTP          = "http/protobuf"
	envOTelHost               = "LOCO_OTEL_HOST"
	envOTelGRPCPort           = "LOCO_OTEL_GRPC_PORT"
	envOTelHTTPPort           = "LOCO_OTEL_HTTP_PORT"
	collectorHostFormat       = "%s.%s.svc.cluster.local"
	otlpHTTPScheme            = "http"
	attributeEnvironmentName  = "deployment.environment.name"
	attributeDeploymentID     = "loco.deployment.id"
	attributeSeparator        = ","
	attributeAssignment       = "="
)

var errTelemetryIncomplete = errors.New(
	"telemetry requires the observability namespace, the collector service and its OTLP gRPC and HTTP ports",
)

// TelemetryConfig locates the OpenTelemetry collector that app pods export to.
type TelemetryConfig struct {
	ScrapeNamespace  string
	ScrapeCollector  string
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

func (c TelemetryConfig) collectorHost() string {
	return fmt.Sprintf(collectorHostFormat, c.CollectorService, c.Namespace)
}

func (c TelemetryConfig) otlpHTTPEndpoint() string {
	host := c.collectorHost()
	httpPort := formatPort(c.HTTPPort)
	hostPort := net.JoinHostPort(host, httpPort)
	endpoint := url.URL{Scheme: otlpHTTPScheme, Host: hostPort}
	return endpoint.String()
}

func formatPort(port int32) string {
	return strconv.Itoa(int(port))
}

func collectorEnvVars(telemetry TelemetryConfig) []corev1.EnvVar {
	host := telemetry.collectorHost()
	grpcPort := formatPort(telemetry.GRPCPort)
	httpPort := formatPort(telemetry.HTTPPort)
	return []corev1.EnvVar{
		{Name: envOTelHost, Value: host},
		{Name: envOTelGRPCPort, Value: grpcPort},
		{Name: envOTelHTTPPort, Value: httpPort},
	}
}

func openTelemetryEnvVars(
	locoRes *locov1alpha1.Application, telemetry TelemetryConfig, secretNames []string,
) []corev1.EnvVar {
	endpoint := telemetry.otlpHTTPEndpoint()
	serviceName := getName(locoRes)
	settings := [][]corev1.EnvVar{
		{
			{Name: envOTLPEndpoint, Value: endpoint},
			{Name: envOTLPProtocol, Value: otlpProtocolHTTP},
		},
		{{Name: envOTelServiceName, Value: serviceName}},
		resourceAttributeVars(locoRes),
	}
	userEnv := locoRes.Spec.ServiceSpec.Deployment.Env
	var vars []corev1.EnvVar
	for _, setting := range settings {
		if userSetsAny(userEnv, secretNames, setting) {
			continue
		}
		vars = append(vars, setting...)
	}
	return vars
}

func userSetsAny(userEnv map[string]string, secretNames []string, setting []corev1.EnvVar) bool {
	for _, envVar := range setting {
		_, plain := userEnv[envVar.Name]
		if plain || slices.Contains(secretNames, envVar.Name) {
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
