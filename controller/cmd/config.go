package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/team-loco/loco/controller/internal/builds"
	"github.com/team-loco/loco/controller/internal/controller"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
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
	envRegistryAuth           = "LOCO_REGISTRY_AUTH"
	envGateway                = "LOCO_GATEWAY"
	envWorkspacePrefix        = "LOCO_WORKSPACE_NAMESPACE_PREFIX"
	envMinReadySeconds        = "LOCO_MIN_READY_SECONDS"
	decimalBase               = 10
	portBits                  = 16
	int32Bits                 = 32
)

var (
	errInvalidRegistryAuth = errors.New(
		"must be " + locov1alpha1.RegistryAuthStatic + " or " + locov1alpha1.RegistryAuthToken,
	)
	errNegativeSeconds = errors.New("must not be negative")
)

type operatorConfig struct {
	RuntimeRecoveryWindow    time.Duration
	LocoNamespace            string
	PullSecretName           string
	RegistryTokenMode        bool
	Gateway                  string
	WorkspaceNamespacePrefix string
	RawBuildConfig           string
	Telemetry                controller.TelemetryConfig
	// MinReadySeconds is nil when unset; only the application reconciler requires it.
	MinReadySeconds *int32
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
		RuntimeRecoveryWindow:    time.Minute,
		LocoNamespace:            os.Getenv(envLocoNamespace),
		PullSecretName:           os.Getenv(envRegistryPullSecretName),
		RegistryTokenMode:        registryTokenMode(),
		Gateway:                  os.Getenv(envGateway),
		WorkspaceNamespacePrefix: os.Getenv(envWorkspacePrefix),
		RawBuildConfig:           os.Getenv(builds.EnvConfig),
		Telemetry:                telemetry,
		MinReadySeconds:          secondsEnv(envMinReadySeconds),
	}
}

func secondsEnv(name string) *int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	seconds, err := strconv.ParseInt(raw, decimalBase, int32Bits)
	if err != nil {
		panic(fmt.Errorf("%s: %w", name, err))
	}
	if seconds < 0 {
		panic(fmt.Errorf("%s %d %w", name, seconds, errNegativeSeconds))
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
