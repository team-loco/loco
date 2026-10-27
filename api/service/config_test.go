package service

import (
	"slices"
	"testing"

	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
)

func TestGetConfigReturnsTheConfiguredDefaults(t *testing.T) {
	defaults := testServiceDefaults()
	const schemaURL = "https://loco.test/schemas/loco.v1.json"
	server := NewConfigServer("v1.2.3", schemaURL, defaults, nil, nil)
	req := &configv1.GetConfigRequest{}
	resp, err := server.GetConfig(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	got := resp.GetServiceDefaults()
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
	if routing.GetPort() != defaults.Port {
		t.Errorf("port = %d, want %d", routing.GetPort(), defaults.Port)
	}
	health := got.GetHealthCheck()
	if health.GetPath() != defaults.HealthPath ||
		health.GetIntervalSeconds() != defaults.HealthInterval ||
		health.GetTimeoutSeconds() != defaults.HealthTimeout ||
		health.GetFailureThreshold() != defaults.HealthFailThreshold ||
		health.GetInitialDelaySeconds() != defaults.HealthStartupGracePeriod {
		t.Errorf("health check = %v, want the configured defaults", health)
	}
	if resp.GetSchemaUrl() != schemaURL {
		t.Errorf("schema url = %q, want %q", resp.GetSchemaUrl(), schemaURL)
	}
	if resp.GetMinCliVersion() != "v1.2.3" {
		t.Errorf("min CLI version %q", resp.GetMinCliVersion())
	}
}

func TestGetConfigReturnsOrgLinks(t *testing.T) {
	links := []OrgLink{
		{Label: "Support", URL: "https://support.example.com/orgs/{orgId}"},
		{Label: "Status", URL: "https://status.example.com"},
	}
	server := NewConfigServer("", "", testServiceDefaults(), nil, links)
	req := &configv1.GetConfigRequest{}
	resp, err := server.GetConfig(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	got := make([]OrgLink, 0, len(resp.GetOrgLinks()))
	for _, link := range resp.GetOrgLinks() {
		got = append(got, OrgLink{Label: link.GetLabel(), URL: link.GetUrl()})
	}
	if !slices.Equal(got, links) {
		t.Errorf("org links = %+v, want %+v", got, links)
	}
}
