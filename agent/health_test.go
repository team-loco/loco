package main

import (
	"testing"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const testControllerProblem = "controller deployment is not ready"

func TestWithSyncStateReportsTheSyncStream(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		connected bool
		want      string
	}{
		{name: "connected", connected: true, want: ""},
		{
			name:      "connected with another problem",
			message:   testControllerProblem,
			connected: true,
			want:      testControllerProblem,
		},
		{name: "disconnected", want: syncDisconnectedMessage},
		{
			name:    "disconnected with another problem",
			message: testControllerProblem,
			want:    testControllerProblem + "; " + syncDisconnectedMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inspected := &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true, Message: tt.message}
			health := withSyncState(inspected, tt.connected)
			if health.GetSyncConnected() != tt.connected {
				t.Errorf("sync connected = %t, want %t", health.GetSyncConnected(), tt.connected)
			}
			if health.GetMessage() != tt.want {
				t.Errorf("message = %q, want %q", health.GetMessage(), tt.want)
			}
		})
	}
}

func TestSetSyncConnectedSignalsEachChange(t *testing.T) {
	a := &Agent{healthChanged: make(chan struct{}, 1)}

	a.setSyncConnected(true)
	if !signaled(a) {
		t.Fatal("connecting the sync stream did not request a heartbeat")
	}
	a.setSyncConnected(true)
	if signaled(a) {
		t.Fatal("a repeated connected state requested a heartbeat")
	}
	a.setSyncConnected(false)
	if !signaled(a) {
		t.Fatal("losing the sync stream did not request a heartbeat")
	}
	if a.syncConnected.Load() {
		t.Fatal("sync stream still reads connected")
	}
}

func signaled(a *Agent) bool {
	select {
	case <-a.healthChanged:
		return true
	default:
		return false
	}
}
