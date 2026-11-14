package main

import (
	"errors"
	"testing"
	"time"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestOperatorConfigReadsTheTelemetrySettings(t *testing.T) {
	t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
	t.Setenv(envObservabilityNamespace, "telemetry")
	t.Setenv(envOTelCollectorService, "collector")
	t.Setenv(envOTelCollectorGRPCPort, "14317")
	t.Setenv(envOTelCollectorHTTPPort, "14318")

	telemetry := newOperatorConfig().Telemetry
	if telemetry.Namespace != "telemetry" || telemetry.CollectorService != "collector" {
		t.Errorf("telemetry = %+v", telemetry)
	}
	if telemetry.GRPCPort != 14317 || telemetry.HTTPPort != 14318 {
		t.Errorf("ports = %d, %d, want 14317, 14318", telemetry.GRPCPort, telemetry.HTTPPort)
	}
}

func TestOperatorConfigReadsTheGatewayAndWorkspacePrefix(t *testing.T) {
	t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
	t.Setenv(envGateway, "edge")
	t.Setenv(envWorkspacePrefix, "tenant-")
	operator := newOperatorConfig()
	if operator.Gateway != "edge" || operator.WorkspaceNamespacePrefix != "tenant-" {
		t.Errorf("gateway = %q, workspace prefix = %q", operator.Gateway, operator.WorkspaceNamespacePrefix)
	}
}

func TestOperatorConfigReadsMinReadySeconds(t *testing.T) {
	t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
	t.Setenv(envMinReadySeconds, "0")
	if got := newOperatorConfig().MinReadySeconds; got == nil || *got != 0 {
		t.Errorf("MinReadySeconds = %v, want 0", got)
	}
	t.Setenv(envMinReadySeconds, "")
	if got := newOperatorConfig().MinReadySeconds; got != nil {
		t.Errorf("unset MinReadySeconds = %d, want nil", *got)
	}
}

func TestOperatorConfigPanicsOnInvalidMinReadySeconds(t *testing.T) {
	for _, value := range []string{"10s", "-1", "4294967296"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
			t.Setenv(envMinReadySeconds, value)
			defer func() {
				if recover() == nil {
					t.Errorf("newOperatorConfig accepted min ready seconds %q", value)
				}
			}()
			newOperatorConfig()
		})
	}
}

func TestOperatorConfigLeavesUnsetPortsZero(t *testing.T) {
	t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
	t.Setenv(envOTelCollectorGRPCPort, "")
	t.Setenv(envOTelCollectorHTTPPort, "")
	telemetry := newOperatorConfig().Telemetry
	if telemetry.GRPCPort != 0 || telemetry.HTTPPort != 0 {
		t.Errorf("ports = %d, %d, want 0, 0", telemetry.GRPCPort, telemetry.HTTPPort)
	}
}

func TestOperatorConfigPanicsOnAnInvalidPort(t *testing.T) {
	for _, value := range []string{"http", "-1", "70000"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
			t.Setenv(envOTelCollectorHTTPPort, value)
			defer func() {
				if recover() == nil {
					t.Errorf("newOperatorConfig accepted port %q", value)
				}
			}()
			newOperatorConfig()
		})
	}
}

func TestOperatorConfigReadsTheRegistryAuthMode(t *testing.T) {
	cases := map[string]bool{locov1alpha1.RegistryAuthStatic: false, locov1alpha1.RegistryAuthToken: true}
	for value, want := range cases {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envRegistryAuth, value)
			if got := newOperatorConfig().RegistryTokenMode; got != want {
				t.Errorf("RegistryTokenMode = %v, want %v", got, want)
			}
		})
	}
}

func TestOperatorConfigPanicsOnAnInvalidRegistryAuthMode(t *testing.T) {
	for _, value := range []string{"", "htpasswd", "Token"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envRegistryAuth, value)
			defer func() {
				err, ok := recover().(error)
				if !ok || !errors.Is(err, errInvalidRegistryAuth) {
					t.Errorf(
						"newOperatorConfig with %s=%q: recovered %v, want %v",
						envRegistryAuth, value, err, errInvalidRegistryAuth,
					)
				}
			}()
			newOperatorConfig()
		})
	}
}

func TestRuntimeRecoveryWindowIsSetAtStartup(t *testing.T) {
	t.Setenv(envRegistryAuth, locov1alpha1.RegistryAuthStatic)
	config := newOperatorConfig()
	if config.RuntimeRecoveryWindow != time.Minute {
		t.Fatalf("recovery window = %s, want one minute", config.RuntimeRecoveryWindow)
	}
}
