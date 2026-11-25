package main

import "testing"

func TestNewAPIConfigEnablesMetricsAndLogsWithTheirOTLPEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		wantMetrics bool
		wantLogs    bool
	}{
		{"no endpoint", "", false, false},
		{"shared endpoint", otlpEndpointEnv, true, true},
		{"metrics endpoint", otlpMetricsEndpointEnv, true, false},
		{"logs endpoint", otlpLogsEndpointEnv, false, true},
		{"traces endpoint", otlpTracesEndpointEnv, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			if tt.env != "" {
				t.Setenv(tt.env, "http://collector.loco.test:4318")
			}
			ac := newAPIConfig()
			if ac.MetricsEnabled != tt.wantMetrics || ac.LogsEnabled != tt.wantLogs {
				t.Fatalf("metrics, logs enabled = %t, %t, want %t, %t",
					ac.MetricsEnabled, ac.LogsEnabled, tt.wantMetrics, tt.wantLogs)
			}
		})
	}
}
