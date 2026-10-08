package main

import (
	"os"

	"github.com/team-loco/loco/controller/internal/builds"
)

const (
	envLocoNamespace          = "LOCO_NAMESPACE"
	envObservabilityNamespace = "LOCO_OBSERVABILITY_NAMESPACE"
	envRegistryPullSecretName = "REGISTRY_PULL_SECRET_NAME" //nolint:gosec
)

type operatorConfig struct {
	LocoNamespace          string
	ObservabilityNamespace string
	PullSecretName         string
	RawBuildConfig         string
}

func newOperatorConfig() operatorConfig {
	return operatorConfig{
		LocoNamespace:          os.Getenv(envLocoNamespace),
		ObservabilityNamespace: os.Getenv(envObservabilityNamespace),
		PullSecretName:         os.Getenv(envRegistryPullSecretName),
		RawBuildConfig:         os.Getenv(builds.EnvConfig),
	}
}
