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
	envOTLPEndpoint           = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPProtocol           = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTelServiceName        = "OTEL_SERVICE_NAME"
	envOTelResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
	otlpProtocolHTTP          = "http/protobuf"
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

func openTelemetryEnvVars(
	app *locov1alpha1.Application, telemetry TelemetryConfig, secretNames []string,
) []corev1.EnvVar {
	if !app.Spec.ServiceSpec.Deployment.Observability.InjectOtel {
		return nil
	}
	defaults := append(resourceAttributeVars(app),
		corev1.EnvVar{Name: envOTLPEndpoint, Value: telemetry.otlpHTTPEndpoint()},
		corev1.EnvVar{Name: envOTLPProtocol, Value: otlpProtocolHTTP},
		corev1.EnvVar{Name: envOTelServiceName, Value: app.Spec.Slug},
	)
	// A variable the application sets as plain env or a secret replaces only the default of the same name.
	return slices.DeleteFunc(defaults, func(v corev1.EnvVar) bool {
		_, plain := app.Spec.ServiceSpec.Deployment.Env[v.Name]
		return plain || slices.Contains(secretNames, v.Name)
	})
}

func resourceAttributeVars(app *locov1alpha1.Application) []corev1.EnvVar {
	attributes := []struct{ key, value string }{
		{key: attributeEnvironmentName, value: app.Spec.EnvironmentName},
		{key: attributeDeploymentID, value: app.Spec.DeploymentID},
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
