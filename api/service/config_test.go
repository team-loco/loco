package service

import (
	"testing"

	"connectrpc.com/connect"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
)

func TestGetConfigReturnsTheConfiguredDefaults(t *testing.T) {
	defaults := testServiceDefaults()
	server := NewConfigServer("onloco.test", "v1.2.3", defaults)
	req := connect.NewRequest(&configv1.GetConfigRequest{})
	resp, err := server.GetConfig(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	got := resp.Msg.GetServiceDefaults()
	if got.GetCpu() != defaults.CPU || got.GetMemory() != defaults.Memory {
		t.Errorf("cpu %q, memory %q, want %q and %q", got.GetCpu(), got.GetMemory(), defaults.CPU, defaults.Memory)
	}
	if got.GetMinReplicas() != defaults.MinReplicas || got.GetMaxReplicas() != defaults.MaxReplicas {
		t.Errorf("replicas %d-%d, want %d-%d",
			got.GetMinReplicas(), got.GetMaxReplicas(), defaults.MinReplicas, defaults.MaxReplicas)
	}
	routing := got.GetRouting()
	if routing.GetPathPrefix() != defaults.PathPrefix || routing.GetIdleTimeout() != defaults.IdleTimeout {
		t.Errorf("routing = %v, want path prefix %q and idle timeout %d",
			routing, defaults.PathPrefix, defaults.IdleTimeout)
	}
	if got.GetPlatformDomain() != "onloco.test" || resp.Msg.GetMinCliVersion() != "v1.2.3" {
		t.Errorf("platform domain %q, min CLI version %q", got.GetPlatformDomain(), resp.Msg.GetMinCliVersion())
	}
}
