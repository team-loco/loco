package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/team-loco/loco/controller/internal/builds"
	"github.com/team-loco/loco/controller/internal/controller"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	envLocoNamespace          = "LOCO_NAMESPACE"
	envObservabilityNamespace = "LOCO_OBSERVABILITY_NAMESPACE"
	envOTelCollectorService   = "LOCO_OTEL_COLLECTOR_SERVICE"
	envOTelCollectorGRPCPort  = "LOCO_OTEL_COLLECTOR_GRPC_PORT"
	envOTelCollectorHTTPPort  = "LOCO_OTEL_COLLECTOR_HTTP_PORT"
	envRegistryPullSecretName = "REGISTRY_PULL_SECRET_NAME" //nolint:gosec
	envRegistryAuth           = "LOCO_REGISTRY_AUTH"
	envGateway                = "LOCO_GATEWAY"
	envWorkspacePrefix        = "LOCO_WORKSPACE_NAMESPACE_PREFIX"
	envMinReadySeconds        = "LOCO_MIN_READY_SECONDS"
	envDrainSeconds           = "LOCO_DRAIN_SECONDS"
	envHealthyAfterSeconds    = "LOCO_HEALTHY_AFTER_SECONDS"
	envOTLPEndpoint           = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPTracesEndpoint     = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envOTLPLogsEndpoint       = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	envOTLPMetricsEndpoint    = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	decimalBase               = 10
	portBits                  = 16
	int31Bits                 = 31
)

var errInvalidRegistryAuth = errors.New(
	"must be " + locov1alpha1.RegistryAuthStatic + " or " + locov1alpha1.RegistryAuthToken,
)

type operatorConfig struct {
	LocoNamespace            string
	PullSecretName           string
	RegistryTokenMode        bool
	Gateway                  string
	WorkspaceNamespacePrefix string
	RawBuildConfig           string
	Telemetry                controller.TelemetryConfig
	// Tracing exports the controller's own traces over OTLP.
	Tracing bool
	// Metrics exports the controller's own metrics over OTLP.
	Metrics bool
	Logs    bool
	// Rollout timings are nil when unset; only the application reconciler requires them.
	MinReadySeconds     *int32
	DrainSeconds        *int32
	HealthyAfterSeconds *int32
}

func newOperatorConfig() operatorConfig {
	drainSeconds := secondsEnv(envDrainSeconds)
	if drainSeconds != nil && *drainSeconds == 0 {
		panic(fmt.Errorf("%s must be positive", envDrainSeconds))
	}
	telemetry := controller.TelemetryConfig{
		Namespace:        os.Getenv(envObservabilityNamespace),
		CollectorService: os.Getenv(envOTelCollectorService),
		GRPCPort:         portEnv(envOTelCollectorGRPCPort),
		HTTPPort:         portEnv(envOTelCollectorHTTPPort),
	}
	return operatorConfig{
		LocoNamespace:            os.Getenv(envLocoNamespace),
		PullSecretName:           os.Getenv(envRegistryPullSecretName),
		RegistryTokenMode:        registryTokenMode(),
		Gateway:                  os.Getenv(envGateway),
		WorkspaceNamespacePrefix: os.Getenv(envWorkspacePrefix),
		RawBuildConfig:           os.Getenv(builds.EnvConfig),
		Telemetry:                telemetry,
		Tracing:                  os.Getenv(envOTLPEndpoint) != "" || os.Getenv(envOTLPTracesEndpoint) != "",
		Logs:                     os.Getenv(envOTLPEndpoint) != "" || os.Getenv(envOTLPLogsEndpoint) != "",
		Metrics:                  os.Getenv(envOTLPEndpoint) != "" || os.Getenv(envOTLPMetricsEndpoint) != "",
		MinReadySeconds:          secondsEnv(envMinReadySeconds),
		DrainSeconds:             drainSeconds,
		HealthyAfterSeconds:      secondsEnv(envHealthyAfterSeconds),
	}
}

func secondsEnv(name string) *int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	seconds, err := strconv.ParseUint(raw, decimalBase, int31Bits)
	if err != nil {
		panic(fmt.Errorf("%s: %w", name, err))
	}
	return new(int32(seconds))
}

func portEnv(name string) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return 0
	}
	port, err := strconv.ParseUint(raw, decimalBase, portBits)
	if err != nil {
		panic(fmt.Errorf("%s: %w", name, err))
	}
	return int32(port)
}

func registryTokenMode() bool {
	raw := os.Getenv(envRegistryAuth)
	switch raw {
	case locov1alpha1.RegistryAuthStatic:
		return false
	case locov1alpha1.RegistryAuthToken:
		return true
	default:
		panic(fmt.Errorf("%s %q %w", envRegistryAuth, raw, errInvalidRegistryAuth))
	}
}
