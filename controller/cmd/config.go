package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/team-loco/loco/controller/internal/builds"
	"github.com/team-loco/loco/controller/internal/controller"
)

const (
	envLocoNamespace          = "LOCO_NAMESPACE"
	envObservabilityNamespace = "LOCO_OBSERVABILITY_NAMESPACE"
	envOTelCollectorService   = "LOCO_OTEL_COLLECTOR_SERVICE"
	envOTelCollectorGRPCPort  = "LOCO_OTEL_COLLECTOR_GRPC_PORT"
	envOTelCollectorHTTPPort  = "LOCO_OTEL_COLLECTOR_HTTP_PORT"
	envRegistryPullSecretName = "REGISTRY_PULL_SECRET_NAME" //nolint:gosec
	decimalBase               = 10
	portBits                  = 16
)

type operatorConfig struct {
	LocoNamespace  string
	PullSecretName string
	RawBuildConfig string
	Telemetry      controller.TelemetryConfig
}

func newOperatorConfig() operatorConfig {
	telemetry := controller.TelemetryConfig{
		Namespace:        os.Getenv(envObservabilityNamespace),
		CollectorService: os.Getenv(envOTelCollectorService),
		GRPCPort:         portEnv(envOTelCollectorGRPCPort),
		HTTPPort:         portEnv(envOTelCollectorHTTPPort),
	}
	return operatorConfig{
		LocoNamespace:  os.Getenv(envLocoNamespace),
		PullSecretName: os.Getenv(envRegistryPullSecretName),
		RawBuildConfig: os.Getenv(builds.EnvConfig),
		Telemetry:      telemetry,
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
