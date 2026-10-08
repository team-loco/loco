package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"time"
)

const (
	envControlPlaneURL          = "CONTROL_PLANE_URL"
	envAgentToken               = "AGENT_TOKEN"
	envKubeconfig               = "KUBECONFIG"
	envNamespace                = "LOCO_NAMESPACE"
	envControllerNamespace      = "LOCO_CONTROLLER_NAMESPACE"
	envControllerDeployment     = "LOCO_CONTROLLER_DEPLOYMENT"
	envBuildNamespace           = "LOCO_BUILD_NAMESPACE"
	envInventoryInterval        = "LOCO_INVENTORY_INTERVAL"
	envBuildRetention           = "LOCO_BUILD_RETENTION"
	envBuildCollectInterval     = "LOCO_BUILD_COLLECT_INTERVAL"
	envHeartbeatInterval        = "LOCO_HEARTBEAT_INTERVAL"
	envClusterQueryTimeout      = "LOCO_CLUSTER_QUERY_TIMEOUT"
	envReconcileWorkers         = "LOCO_RECONCILE_WORKERS"
	envReconcileRetryBaseDelay  = "LOCO_RECONCILE_RETRY_BASE_DELAY"
	envReconcileRetryMaxDelay   = "LOCO_RECONCILE_RETRY_MAX_DELAY"
	envSyncOutboundBuffer       = "LOCO_SYNC_OUTBOUND_BUFFER"
	envBuildQueueSize           = "LOCO_BUILD_QUEUE_SIZE"
	envBuildCreateRetryDelay    = "LOCO_BUILD_CREATE_RETRY_DELAY"
	envBuildCreateRetryAttempts = "LOCO_BUILD_CREATE_RETRY_ATTEMPTS"
	envReconnectBaseDelay       = "LOCO_RECONNECT_BASE_DELAY"
	envReconnectMaxDelay        = "LOCO_RECONNECT_MAX_DELAY"
	envHealthyStreamDuration    = "LOCO_HEALTHY_STREAM_DURATION"
	develVersion                = "(devel)"
)

var (
	errMissingEnv      = errors.New("is required")
	errInvalidDuration = errors.New("must be a positive duration")
	errInvalidInteger  = errors.New("must be a positive integer")
)

var version string

type Config struct {
	ControlPlaneURL          string
	AgentToken               string
	AgentVersion             string
	Kubeconfig               string
	Namespace                string
	ControllerNamespace      string
	ControllerDeployment     string
	BuildNamespace           string
	InventoryInterval        time.Duration
	BuildRetention           time.Duration
	BuildCollectInterval     time.Duration
	HeartbeatInterval        time.Duration
	ClusterQueryTimeout      time.Duration
	ReconcileWorkers         int
	ReconcileRetryBaseDelay  time.Duration
	ReconcileRetryMaxDelay   time.Duration
	SyncOutboundBuffer       int
	BuildQueueSize           int
	BuildCreateRetryDelay    time.Duration
	BuildCreateRetryAttempts int
	ReconnectBaseDelay       time.Duration
	ReconnectMaxDelay        time.Duration
	HealthyStreamDuration    time.Duration
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
	cfg := &Config{
		AgentVersion: agentVersion,
		Kubeconfig:   getenv(envKubeconfig),
	}
	required := map[string]*string{
		envControlPlaneURL:      &cfg.ControlPlaneURL,
		envAgentToken:           &cfg.AgentToken,
		envNamespace:            &cfg.Namespace,
		envControllerDeployment: &cfg.ControllerDeployment,
		envBuildNamespace:       &cfg.BuildNamespace,
	}
	for name, field := range required {
		value, err := requireEnv(getenv, name)
		if err != nil {
			return nil, err
		}
		*field = value
	}
	cfg.ControllerNamespace = getenv(envControllerNamespace)
	if cfg.ControllerNamespace == "" {
		cfg.ControllerNamespace = cfg.Namespace
	}

	durations := map[string]*time.Duration{
		envInventoryInterval:       &cfg.InventoryInterval,
		envBuildRetention:          &cfg.BuildRetention,
		envBuildCollectInterval:    &cfg.BuildCollectInterval,
		envHeartbeatInterval:       &cfg.HeartbeatInterval,
		envClusterQueryTimeout:     &cfg.ClusterQueryTimeout,
		envReconcileRetryBaseDelay: &cfg.ReconcileRetryBaseDelay,
		envReconcileRetryMaxDelay:  &cfg.ReconcileRetryMaxDelay,
		envBuildCreateRetryDelay:   &cfg.BuildCreateRetryDelay,
		envReconnectBaseDelay:      &cfg.ReconnectBaseDelay,
		envReconnectMaxDelay:       &cfg.ReconnectMaxDelay,
		envHealthyStreamDuration:   &cfg.HealthyStreamDuration,
	}
	for name, field := range durations {
		raw, err := requireEnv(getenv, name)
		if err != nil {
			return nil, err
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%s %q %w", name, raw, errInvalidDuration)
		}
		*field = parsed
	}

	integers := map[string]*int{
		envReconcileWorkers:         &cfg.ReconcileWorkers,
		envSyncOutboundBuffer:       &cfg.SyncOutboundBuffer,
		envBuildQueueSize:           &cfg.BuildQueueSize,
		envBuildCreateRetryAttempts: &cfg.BuildCreateRetryAttempts,
	}
	for name, field := range integers {
		raw, err := requireEnv(getenv, name)
		if err != nil {
			return nil, err
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%s %q %w", name, raw, errInvalidInteger)
		}
		*field = parsed
	}
	return cfg, nil
}

func requireEnv(getenv func(string) string, name string) (string, error) {
	value := getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s %w", name, errMissingEnv)
	}
	return value, nil
}
