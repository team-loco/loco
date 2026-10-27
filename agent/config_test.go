package main

import (
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/agent/pkg/telemetrytoken"
	"github.com/team-loco/loco/internal/loglevel"
)

const (
	testAgentVersion     = "sha-test"
	testIngestSecret     = "loco-otlp-exporter" //nolint:gosec
	testIngestSecretKey  = "token"
	testIngestNamespaces = "observability,observability-node"
)

func agentEnv() map[string]string {
	return map[string]string{
		envControlPlaneURL:          "https://api.loco.test",
		envKeySecret:                "agent-identity",
		envAssertionLifetime:        "90s",
		envNamespace:                "loco-system",
		envControllerDeployment:     "loco-controller",
		envBuildNamespace:           "loco-builds",
		envInventoryInterval:        "10m",
		envBuildRetention:           "1h",
		envBuildCollectInterval:     "5m",
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
		envLogLevel:                 "warn",
		envGateway:                  "ingress",
		envAppDomains:               "apps.test,other.test",
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
	env[envJoinToken] = "loco_j_join"
	env[envCertificate] = "wildcard"
	cfg, err := parseAgentConfig(lookup(env), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	want := Config{
		LogLevel:                 slog.LevelWarn,
		ControlPlaneURL:          "https://api.loco.test",
		JoinToken:                "loco_j_join",
		KeySecret:                "agent-identity",
		AssertionLifetime:        90 * time.Second,
		AgentVersion:             testAgentVersion,
		Namespace:                "loco-system",
		ControllerNamespace:      "loco-operator",
		ControllerDeployment:     "loco-controller",
		BuildNamespace:           "loco-builds",
		InventoryInterval:        10 * time.Minute,
		BuildRetention:           time.Hour,
		BuildCollectInterval:     5 * time.Minute,
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
		Gateway:                  "ingress",
		Certificate:              "wildcard",
		AppDomains:               []string{"apps.test", "other.test"},
	}
	if !reflect.DeepEqual(*cfg, want) {
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
		envKeySecret,
		envAssertionLifetime,
		envNamespace,
		envControllerDeployment,
		envBuildNamespace,
		envInventoryInterval,
		envBuildRetention,
		envBuildCollectInterval,
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
		envLogLevel,
		envGateway,
		envAppDomains,
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
		envClusterQueryTimeout,
		envReconcileRetryBaseDelay,
		envReconcileRetryMaxDelay,
		envBuildCreateRetryDelay,
		envReconnectBaseDelay,
		envReconnectMaxDelay,
		envHealthyStreamDuration,
		envAssertionLifetime,
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

func TestParseAgentConfigRejectsUnknownLogLevel(t *testing.T) {
	for _, value := range []string{"-4", "verbose"} {
		t.Run(value, func(t *testing.T) {
			env := agentEnv()
			env[envLogLevel] = value
			if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, loglevel.ErrUnknown) {
				t.Errorf("parseAgentConfig with %s=%s = %v, want loglevel.ErrUnknown", envLogLevel, value, err)
			}
		})
	}
}

func TestParseAgentConfigReadsTheIngestTokenSecret(t *testing.T) {
	cfg, err := parseAgentConfig(lookup(agentEnv()), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg.IngestTokenSecret, telemetrytoken.Secret{}) {
		t.Fatalf("ingest token Secret = %+v, want none when %s is unset", cfg.IngestTokenSecret, envIngestTokenSecret)
	}
	env := agentEnv()
	env[envIngestTokenSecret] = testIngestSecret
	env[envIngestTokenSecretKey] = testIngestSecretKey
	env[envIngestTokenNamespaces] = testIngestNamespaces
	cfg, err = parseAgentConfig(lookup(env), testAgentVersion)
	if err != nil {
		t.Fatalf("parseAgentConfig: %v", err)
	}
	want := telemetrytoken.Secret{
		Namespaces: strings.Split(testIngestNamespaces, listSeparator),
		Name:       testIngestSecret,
		Key:        testIngestSecretKey,
	}
	if !reflect.DeepEqual(cfg.IngestTokenSecret, want) {
		t.Fatalf("ingest token Secret = %+v, want %+v", cfg.IngestTokenSecret, want)
	}
	for _, name := range []string{envIngestTokenSecretKey, envIngestTokenNamespaces} {
		missing := agentEnv()
		missing[envIngestTokenSecret] = testIngestSecret
		missing[envIngestTokenSecretKey] = testIngestSecretKey
		missing[envIngestTokenNamespaces] = testIngestNamespaces
		delete(missing, name)
		if _, err := parseAgentConfig(lookup(missing), testAgentVersion); !errors.Is(err, errMissingEnv) {
			t.Errorf("parseAgentConfig without %s = %v, want errMissingEnv", name, err)
		}
	}
}
