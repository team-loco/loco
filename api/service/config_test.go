package service

import (
	"slices"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestGetConfigReturnsTheConfiguredDefaults(t *testing.T) {
	defaults := testServiceDefaults()
	defaults.LogRetention = 48 * time.Hour
	const schemaURL = "https://loco.test/schemas/loco.v1.json"
	server := NewConfigServer("v1.2.3", schemaURL, defaults, nil, nil, 42*time.Hour)
	req := &configv1.GetConfigRequest{}
	resp, err := server.GetConfig(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if resp.GetApiTokenMaxLifetimeSeconds() != int64((42*time.Hour)/time.Second) {
		t.Errorf("API token maximum = %d, want 42 hours", resp.GetApiTokenMaxLifetimeSeconds())
	}
	got := resp.GetServiceDefaults()
	if got.GetCpuTarget() != defaults.CPUTarget {
		t.Fatalf("CPU target = %d, want %d", got.GetCpuTarget(), defaults.CPUTarget)
	}
	if got.GetObservability().GetLogging().GetRetentionPeriod() != "2d" {
		t.Fatalf("log retention = %q, want 2d", got.GetObservability().GetLogging().GetRetentionPeriod())
	}
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
	if got.GetDeployAttempts() != defaults.DeployAttempts ||
		got.GetDeployRetryDelaySeconds() != defaults.DeployRetryDelay {
		t.Errorf(
			"deploy attempts %d, retry delay %ds, want %d and %ds",
			got.GetDeployAttempts(),
			got.GetDeployRetryDelaySeconds(),
			defaults.DeployAttempts,
			defaults.DeployRetryDelay,
		)
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
	server := NewConfigServer("", "", testServiceDefaults(), nil, links, time.Hour)
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

func TestGetConfigSizeLimitsAreTheValidationBounds(t *testing.T) {
	server := NewConfigServer("", "", testServiceDefaults(), nil, nil, time.Hour)
	resp, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	limits := resp.GetSizeLimits()
	size := func(cpuMillis, memoryBytes int64, minReplicas, maxReplicas int32) locoControllerV1.ResourcesSpec {
		return locoControllerV1.ResourcesSpec{
			CPU:      resource.NewMilliQuantity(cpuMillis, resource.DecimalSI).String(),
			Memory:   resource.NewQuantity(memoryBytes, resource.BinarySI).String(),
			Replicas: locoControllerV1.ReplicasSpec{Min: minReplicas, Max: maxReplicas},
		}
	}
	minCPU, maxCPU := int64(limits.GetMinCpuMillis()), int64(limits.GetMaxCpuMillis())
	minMemory, maxMemory := limits.GetMinMemoryBytes(), limits.GetMaxMemoryBytes()
	minReplicas, maxReplicas := limits.GetMinReplicas(), limits.GetMaxReplicas()

	for name, spec := range map[string]locoControllerV1.ResourcesSpec{
		"minimums": size(minCPU, minMemory, minReplicas, minReplicas),
		"maximums": size(maxCPU, maxMemory, maxReplicas, maxReplicas),
	} {
		if err := spec.Validate(); err != nil {
			t.Errorf("%s: %+v rejected: %v", name, spec, err)
		}
	}
	for name, spec := range map[string]locoControllerV1.ResourcesSpec{
		"cpu below":      size(minCPU-1, minMemory, minReplicas, minReplicas),
		"cpu above":      size(maxCPU+1, minMemory, minReplicas, minReplicas),
		"memory below":   size(minCPU, minMemory-1, minReplicas, minReplicas),
		"memory above":   size(minCPU, maxMemory+1, minReplicas, minReplicas),
		"replicas below": size(minCPU, minMemory, minReplicas-1, minReplicas),
		"replicas above": size(minCPU, minMemory, minReplicas, maxReplicas+1),
	} {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: %+v accepted", name, spec)
		}
	}
}
