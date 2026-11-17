package service

import (
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const configAPIVersion = "v1.2.4"

func TestGetConfigReturnsTheConfiguredDefaults(t *testing.T) {
	t.Parallel()
	defaults := testServiceDefaults()
	const schemaURL = "https://loco.test/schemas/loco.v1.json"
	server := NewConfigServer(configAPIVersion,
		"v1.2.3",
		schemaURL,
		defaults,
		nil,
		nil,
		42*time.Hour,
		6*time.Hour,
		limits.Defaults(),
		nil,
	)
	req := &configv1.GetConfigRequest{}
	resp, err := server.GetConfig(t.Context(), req)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if resp.GetApiVersion() != configAPIVersion {
		t.Fatalf("API version = %s", resp.GetApiVersion())
	}
	if resp.GetApiTokenMaxLifetimeSeconds() != int64((42*time.Hour)/time.Second) {
		t.Errorf("API token maximum = %d, want 42 hours", resp.GetApiTokenMaxLifetimeSeconds())
	}
	if got := resp.GetObservabilityMaxTimeRange().AsDuration(); got != 6*time.Hour {
		t.Errorf("observability range = %v, want 6h", got)
	}
	got := resp.GetServiceDefaults()
	if got.GetRollbackOnFailure() {
		t.Fatal("rollback default must be false")
	}
	if got.GetCpuTarget() != defaults.CPUTarget {
		t.Fatalf("CPU target = %d, want %d", got.GetCpuTarget(), defaults.CPUTarget)
	}
	if got.GetCpu() != defaults.CPU || got.GetMemory() != defaults.Memory {
		t.Errorf("cpu %q, memory %q, want %q and %q", got.GetCpu(), got.GetMemory(), defaults.CPU, defaults.Memory)
	}
	if got.GetMinReplicas() != defaults.MinReplicas || got.GetMaxReplicas() != defaults.MaxReplicas {
		t.Errorf("replicas %d-%d, want %d-%d",
			got.GetMinReplicas(), got.GetMaxReplicas(), defaults.MinReplicas, defaults.MaxReplicas)
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
	t.Parallel()
	links := []OrgLink{
		{Label: "Support", URL: "https://support.example.com/orgs/{orgId}"},
		{Label: "Status", URL: "https://status.example.com"},
	}
	server := NewConfigServer(
		configAPIVersion,
		"",
		"",
		testServiceDefaults(),
		nil,
		links,
		time.Hour,
		6*time.Hour,
		limits.Defaults(),
		nil,
	)
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
	t.Parallel()
	server := NewConfigServer(
		configAPIVersion,
		"",
		"",
		testServiceDefaults(),
		nil,
		nil,
		time.Hour,
		6*time.Hour,
		limits.Defaults(),
		nil,
	)
	resp, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	sizes := resp.GetSizeLimits()
	size := func(cpuMillis, memoryBytes int64, minReplicas, maxReplicas int32) locoControllerV1.ResourcesSpec {
		return locoControllerV1.ResourcesSpec{
			CPU:      resource.NewMilliQuantity(cpuMillis, resource.DecimalSI).String(),
			Memory:   resource.NewQuantity(memoryBytes, resource.BinarySI).String(),
			Replicas: locoControllerV1.ReplicasSpec{Min: minReplicas, Max: maxReplicas},
		}
	}
	minCPU, maxCPU := int64(sizes.GetMinCpuMillis()), int64(sizes.GetMaxCpuMillis())
	minMemory, maxMemory := sizes.GetMinMemoryBytes(), sizes.GetMaxMemoryBytes()
	minReplicas, maxReplicas := sizes.GetMinReplicas(), sizes.GetMaxReplicas()

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

func TestGetConfigReportsTheInstallCountLimits(t *testing.T) {
	t.Parallel()
	counts := limits.Defaults()
	limits.ResourcesPerWorkspace.Set(counts, 7)
	server := NewConfigServer(
		configAPIVersion,
		"",
		"",
		testServiceDefaults(),
		nil,
		nil,
		time.Hour,
		6*time.Hour,
		counts,
		nil,
	)
	resp, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got := resp.GetCountLimits().GetResourcesPerWorkspace(); got != 7 {
		t.Fatalf("resources per workspace = %d, want the configured 7", got)
	}
}

func TestGetConfigReportsInstallHistoryRetention(t *testing.T) {
	t.Parallel()
	for name, images := range map[string]int32{"cleanup disabled": 0, "configured": 7} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			retention := &configv1.HistoryRetention{
				BuildImagesPerResource: images,
				Events:                 durationpb.New(36*time.Hour + 500*time.Millisecond),
			}
			server := NewConfigServer(configAPIVersion,
				"",
				"",
				testServiceDefaults(),
				nil,
				nil,
				time.Hour,
				6*time.Hour,
				limits.Defaults(),
				retention,
			)
			resp, err := server.GetConfig(t.Context(), &configv1.GetConfigRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(resp.GetHistoryRetention(), retention) {
				t.Fatalf("history retention = %v, want %v", resp.GetHistoryRetention(), retention)
			}
		})
	}
}
