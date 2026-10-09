package v1alpha1

import (
	jsonv2 "encoding/json/v2"
	"strings"
	"testing"
)

func TestZeroScalarFieldsAreOmittedUnderJSONV2(t *testing.T) {
	spec := ServiceSpec{
		Deployment: &ServiceDeploymentSpec{
			Image:       "app:latest",
			Port:        8080,
			HealthCheck: &HealthCheckSpec{Path: "/healthz", Interval: 1, Timeout: 1, FailThreshold: 1},
		},
		Resources: &ResourcesSpec{
			CPU:      "100m",
			Memory:   "32Mi",
			Replicas: ReplicasSpec{Min: 1, Max: 1},
			Scalers:  ScalersSpec{CPUTarget: 50},
		},
		Obs: &ObsSpec{
			Logging: LoggingSpec{RetentionPeriod: "7d"},
			Metrics: MetricsSpec{Path: "/metrics"},
			Tracing: TracingSpec{SampleRate: "0.1"},
		},
	}
	values := []any{spec, ApplicationStatus{}}
	absent := []string{
		`"enabled"`, `"memoryTarget"`, `"startupGracePeriod"`,
		`"port":0`, `"structured"`, `"deployedGeneration"`, `"observedPlacementRevision"`,
	}
	for _, value := range values {
		encoded, err := jsonv2.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %T: %v", value, err)
		}
		for _, member := range absent {
			if strings.Contains(string(encoded), member) {
				t.Errorf("%T encodes zero member %s: %s", value, member, encoded)
			}
		}
	}
}
