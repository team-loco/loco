package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/team-loco/loco/controller/internal/builds"
	"github.com/team-loco/loco/controller/internal/controller"
)

const (
	envScrapeNamespace        = "LOCO_METRICS_COLLECTOR_NAMESPACE"
	envScrapeCollector        = "LOCO_METRICS_COLLECTOR_SERVICE"
	envLocoNamespace          = "LOCO_NAMESPACE"
	envObservabilityNamespace = "LOCO_OBSERVABILITY_NAMESPACE"
	envOTelCollectorService   = "LOCO_OTEL_COLLECTOR_SERVICE"
	envOTelCollectorGRPCPort  = "LOCO_OTEL_COLLECTOR_GRPC_PORT"
	envOTelCollectorHTTPPort  = "LOCO_OTEL_COLLECTOR_HTTP_PORT"
	envRegistryPullSecretName = "REGISTRY_PULL_SECRET_NAME" //nolint:gosec
	envGateway                = "LOCO_GATEWAY"
	envWorkspacePrefix        = "LOCO_WORKSPACE_NAMESPACE_PREFIX"
	decimalBase               = 10
	portBits                  = 16
)

type operatorConfig struct {
	LocoNamespace            string
	PullSecretName           string
	Gateway                  string
	WorkspaceNamespacePrefix string
	RawBuildConfig           string
	Telemetry                controller.TelemetryConfig
}

func newOperatorConfig() operatorConfig {
	telemetry := controller.TelemetryConfig{
		ScrapeNamespace:  os.Getenv(envScrapeNamespace),
		ScrapeCollector:  os.Getenv(envScrapeCollector),
		Namespace:        os.Getenv(envObservabilityNamespace),
		CollectorService: os.Getenv(envOTelCollectorService),
		GRPCPort:         portEnv(envOTelCollectorGRPCPort),
		HTTPPort:         portEnv(envOTelCollectorHTTPPort),
	}
	return operatorConfig{
		LocoNamespace:            os.Getenv(envLocoNamespace),
		PullSecretName:           os.Getenv(envRegistryPullSecretName),
		Gateway:                  os.Getenv(envGateway),
		WorkspaceNamespacePrefix: os.Getenv(envWorkspacePrefix),
		RawBuildConfig:           os.Getenv(builds.EnvConfig),
		Telemetry:                telemetry,
	}
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
