package main

import (
	"errors"
	"testing"
	"time"
)

const testAgentVersion = "sha-test"

func agentEnv() map[string]string {
	return map[string]string{
		envControlPlaneURL:          "https://api.loco.test",
		envAgentToken:               "token",
		envNamespace:                "loco-system",
		envControllerDeployment:     "loco-controller",
		envBuildNamespace:           "loco-builds",
		envInventoryInterval:        "10m",
		envBuildRetention:           "1h",
		envBuildCollectInterval:     "5m",
		envHeartbeatInterval:        "31s",
		envClusterQueryTimeout:      "11s",
		envReconcileWorkers:         "8",
		envReconcileRetryBaseDelay:  "2s",
		envReconcileRetryMaxDelay:   "32s",
		envSyncOutboundBuffer:       "256",
		envBuildQueueSize:           "64",
		envBuildCreateRetryDelay:    "3s",
		envBuildCreateRetryAttempts: "5",
		envReconnectBaseDelay:       "4s",
		envReconnectMaxDelay:        "34s",
		envHealthyStreamDuration:    "2m",
	}
}

func lookup(env map[string]string) func(string) string {
	return func(key string) string {
		return env[key]
	}
}

func TestParseAgentConfigReadsTheEnvironment(t *testing.T) {
	env := agentEnv()
	env[envControllerNamespace] = "loco-operator"
	env[envKubeconfig] = "/tmp/kubeconfig"
	cfg, err := parseAgentConfig(lookup(env), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	want := Config{
		ControlPlaneURL:          "https://api.loco.test",
		AgentToken:               "token",
		AgentVersion:             testAgentVersion,
		Namespace:                "loco-system",
		ControllerNamespace:      "loco-operator",
		ControllerDeployment:     "loco-controller",
		BuildNamespace:           "loco-builds",
		InventoryInterval:        10 * time.Minute,
		BuildRetention:           time.Hour,
		BuildCollectInterval:     5 * time.Minute,
		HeartbeatInterval:        31 * time.Second,
		ClusterQueryTimeout:      11 * time.Second,
		ReconcileWorkers:         8,
		ReconcileRetryBaseDelay:  2 * time.Second,
		ReconcileRetryMaxDelay:   32 * time.Second,
		SyncOutboundBuffer:       256,
		BuildQueueSize:           64,
		BuildCreateRetryDelay:    3 * time.Second,
		BuildCreateRetryAttempts: 5,
		ReconnectBaseDelay:       4 * time.Second,
		ReconnectMaxDelay:        34 * time.Second,
		HealthyStreamDuration:    2 * time.Minute,
		Kubeconfig:               "/tmp/kubeconfig",
	}
	if *cfg != want {
		t.Errorf("config = %+v, want %+v", *cfg, want)
	}
}

func TestParseAgentConfigDefaultsTheControllerNamespaceToItsOwn(t *testing.T) {
	cfg, err := parseAgentConfig(lookup(agentEnv()), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	if cfg.ControllerNamespace != cfg.Namespace {
		t.Errorf("controller namespace = %q, want %q", cfg.ControllerNamespace, cfg.Namespace)
	}
}

func TestParseAgentConfigRejectsMissingValues(t *testing.T) {
	required := []string{
		envControlPlaneURL,
		envAgentToken,
		envNamespace,
		envControllerDeployment,
		envBuildNamespace,
		envInventoryInterval,
		envBuildRetention,
		envBuildCollectInterval,
		envHeartbeatInterval,
		envClusterQueryTimeout,
		envReconcileWorkers,
		envReconcileRetryBaseDelay,
		envReconcileRetryMaxDelay,
		envSyncOutboundBuffer,
		envBuildQueueSize,
		envBuildCreateRetryDelay,
		envBuildCreateRetryAttempts,
		envReconnectBaseDelay,
		envReconnectMaxDelay,
		envHealthyStreamDuration,
	}
	for _, name := range required {
		t.Run(name, func(t *testing.T) {
			env := agentEnv()
			delete(env, name)
			if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, errMissingEnv) {
				t.Errorf("parseAgentConfig without %s = %v, want errMissingEnv", name, err)
			}
		})
	}
}

func TestParseAgentConfigRejectsInvalidDurations(t *testing.T) {
	durations := []string{
		envInventoryInterval,
		envBuildRetention,
		envBuildCollectInterval,
		envHeartbeatInterval,
		envClusterQueryTimeout,
		envReconcileRetryBaseDelay,
		envReconcileRetryMaxDelay,
		envBuildCreateRetryDelay,
		envReconnectBaseDelay,
		envReconnectMaxDelay,
		envHealthyStreamDuration,
	}
	for _, name := range durations {
		for _, value := range []string{"soon", "0s", "-1m", "5"} {
			t.Run(name+"="+value, func(t *testing.T) {
				env := agentEnv()
				env[name] = value
				if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, errInvalidDuration) {
					t.Errorf("parseAgentConfig with %s=%q = %v, want errInvalidDuration", name, value, err)
				}
			})
		}
	}
}

func TestParseAgentConfigRejectsInvalidIntegers(t *testing.T) {
	integers := []string{
		envReconcileWorkers,
		envSyncOutboundBuffer,
		envBuildQueueSize,
		envBuildCreateRetryAttempts,
	}
	for _, name := range integers {
		for _, value := range []string{"many", "0", "-1", "1.5", "1s"} {
			t.Run(name+"="+value, func(t *testing.T) {
				env := agentEnv()
				env[name] = value
				if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, errInvalidInteger) {
					t.Errorf("parseAgentConfig with %s=%q = %v, want errInvalidInteger", name, value, err)
				}
			})
		}
	}
}

func TestParseAgentConfigLeavesTheKubeconfigOptional(t *testing.T) {
	cfg, err := parseAgentConfig(lookup(agentEnv()), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	if cfg.Kubeconfig != "" {
		t.Errorf("kubeconfig = %q, want it empty when %s is unset", cfg.Kubeconfig, envKubeconfig)
	}
}

func TestBuildVersionPrefersTheLinkedVersion(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	version = testAgentVersion
	if got := buildVersion(); got != testAgentVersion {
		t.Errorf("buildVersion = %q, want %q", got, testAgentVersion)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("buildVersion without a linked version is empty")
	}
}
