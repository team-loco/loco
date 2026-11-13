package main

import (
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/agent/pkg/telemetrytoken"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/loglevel"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
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
		envClusterRole:              "apps",
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
		envRegistryAuth:             locoControllerV1.RegistryAuthToken,
		envRegistryCheckInterval:    "6m",
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
		Role:                     clusterv1.ClusterRole_CLUSTER_ROLE_APPS,
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
		RegistryAuth:             locoControllerV1.RegistryAuthToken,
		RegistryCheckInterval:    6 * time.Minute,
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
		envClusterRole,
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
		envRegistryAuth,
		envRegistryCheckInterval,
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
		envRegistryCheckInterval,
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

func TestParseAgentConfigReadsTheRegistryAuthMode(t *testing.T) {
	cases := []struct {
		value     string
		tokenMode bool
	}{
		{value: locoControllerV1.RegistryAuthStatic, tokenMode: false},
		{value: locoControllerV1.RegistryAuthToken, tokenMode: true},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			env := agentEnv()
			env[envRegistryAuth] = tc.value
			cfg, err := parseAgentConfig(lookup(env), testAgentVersion)
			if err != nil {
				t.Fatalf("parseAgentConfig: %v", err)
			}
			if cfg.RegistryTokenMode() != tc.tokenMode {
				t.Errorf("token mode with %s=%s = %t, want %t",
					envRegistryAuth, tc.value, cfg.RegistryTokenMode(), tc.tokenMode)
			}
		})
	}
	for _, value := range []string{"Token", "htpasswd"} {
		t.Run(value, func(t *testing.T) {
			env := agentEnv()
			env[envRegistryAuth] = value
			if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, errInvalidAuth) {
				t.Errorf("parseAgentConfig with %s=%s = %v, want errInvalidAuth", envRegistryAuth, value, err)
			}
		})
	}
}

func TestParseAgentConfigReadsTheClusterRole(t *testing.T) {
	for value, builds := range map[string]bool{"apps": false, "builds": true, "all": true} {
		t.Run(value, func(t *testing.T) {
			env := agentEnv()
			env[envClusterRole] = value
			cfg, err := parseAgentConfig(lookup(env), testAgentVersion)
			if err != nil {
				t.Fatalf("parseAgentConfig: %v", err)
			}
			if cfg.Role != clusterRoles[value] || cfg.RunsBuilds() != builds {
				t.Errorf("%s=%s gives role %s, runs builds %t", envClusterRole, value, cfg.Role, cfg.RunsBuilds())
			}
		})
	}
	env := agentEnv()
	env[envClusterRole] = "everything"
	if _, err := parseAgentConfig(lookup(env), testAgentVersion); !errors.Is(err, errInvalidRole) {
		t.Errorf("parseAgentConfig with an unknown role = %v, want errInvalidRole", err)
	}
}
