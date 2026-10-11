package main

import (
	"testing"
)

func TestOperatorConfigReadsTheTelemetrySettings(t *testing.T) {
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

func TestOperatorConfigLeavesUnsetPortsZero(t *testing.T) {
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
