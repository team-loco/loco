package service

import (
	"testing"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestHealthStatusFromProtoOnlyProducesAllowedValues(t *testing.T) {
	tests := []struct {
		name   string
		health *agentv1.AgentHealth
		want   string
	}{
		{name: "no report", health: nil, want: clusterHealthDegraded},
		{
			name:   "all healthy",
			health: &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
			want:   clusterHealthHealthy,
		},
		{
			name:   "controller down",
			health: &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: false},
			want:   clusterHealthDegraded,
		},
		{
			name:   "kubernetes down",
			health: &agentv1.AgentHealth{KubernetesHealthy: false, ControllerHealthy: true},
			want:   clusterHealthDegraded,
		},
		{name: "all down", health: &agentv1.AgentHealth{}, want: clusterHealthUnhealthy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := healthStatusFromProto(tt.health); got != tt.want {
				t.Fatalf("healthStatusFromProto = %q, want %q", got, tt.want)
			}
		})
	}
}
