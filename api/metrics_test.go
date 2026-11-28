package main

import "testing"

func TestNewAPIConfigEnablesMetricsWithItsOTLPEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		wantMetrics bool
	}{
		{"no endpoint", "", false},
		{"shared endpoint", otlpEndpointEnv, true},
		{"metrics endpoint", otlpMetricsEndpointEnv, true},
		{"traces endpoint", otlpTracesEndpointEnv, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			if tt.env != "" {
				t.Setenv(tt.env, "http://collector.loco.test:4318")
			}
			ac := newAPIConfig()
			if ac.MetricsEnabled != tt.wantMetrics {
				t.Fatalf("metrics enabled = %t, want %t", ac.MetricsEnabled, tt.wantMetrics)
			}
		})
	}
}
