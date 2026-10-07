package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"
)

const (
	envControlPlaneURL      = "CONTROL_PLANE_URL"
	envAgentToken           = "AGENT_TOKEN"
	envNamespace            = "LOCO_NAMESPACE"
	envControllerNamespace  = "LOCO_CONTROLLER_NAMESPACE"
	envControllerDeployment = "LOCO_CONTROLLER_DEPLOYMENT"
	envBuildNamespace       = "LOCO_BUILD_NAMESPACE"
	envInventoryInterval    = "LOCO_INVENTORY_INTERVAL"
	envBuildRetention       = "LOCO_BUILD_RETENTION"
	envBuildCollectInterval = "LOCO_BUILD_COLLECT_INTERVAL"
	develVersion            = "(devel)"
)

var (
	errMissingEnv      = errors.New("is required")
	errInvalidDuration = errors.New("must be a positive duration")
)

var version string

type Config struct {
	ControlPlaneURL      string
	AgentToken           string
	AgentVersion         string
	Namespace            string
	ControllerNamespace  string
	ControllerDeployment string
	BuildNamespace       string
	InventoryInterval    time.Duration
	BuildRetention       time.Duration
	BuildCollectInterval time.Duration
}

func newAgentConfig() *Config {
	cfg, err := parseAgentConfig(os.Getenv, buildVersion())
	if err != nil {
		panic(err)
	}
	return cfg
}

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	return info.Main.Version
}

func parseAgentConfig(getenv func(string) string, agentVersion string) (*Config, error) {
	cfg := &Config{AgentVersion: agentVersion}
	required := map[string]*string{
		envControlPlaneURL:      &cfg.ControlPlaneURL,
		envAgentToken:           &cfg.AgentToken,
		envNamespace:            &cfg.Namespace,
		envControllerDeployment: &cfg.ControllerDeployment,
		envBuildNamespace:       &cfg.BuildNamespace,
	}
	for name, field := range required {
		value := getenv(name)
		if value == "" {
			return nil, fmt.Errorf("%s %w", name, errMissingEnv)
		}
		*field = value
	}
	cfg.ControllerNamespace = getenv(envControllerNamespace)
	if cfg.ControllerNamespace == "" {
		cfg.ControllerNamespace = cfg.Namespace
	}

	durations := map[string]*time.Duration{
		envInventoryInterval:    &cfg.InventoryInterval,
		envBuildRetention:       &cfg.BuildRetention,
		envBuildCollectInterval: &cfg.BuildCollectInterval,
	}
	for name, field := range durations {
		raw := getenv(name)
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%s %q %w", name, raw, errInvalidDuration)
		}
		*field = parsed
	}
	return cfg, nil
}
