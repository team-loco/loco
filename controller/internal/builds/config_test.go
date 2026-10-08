package builds

import (
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/team-loco/loco/controller/internal/builds/buildstest"
)

func chartConfig(t *testing.T, overrides map[string]any) string {
	t.Helper()
	raw, err := buildstest.ChartConfigJSON(overrides)
	if err != nil {
		t.Fatalf("read the chart's build values: %v", err)
	}
	return raw
}

func TestParseConfigReadsTheChartValues(t *testing.T) {
	builds, err := buildstest.ChartBuilds()
	if err != nil {
		t.Fatalf("read the chart's build values: %v", err)
	}
	encoded, err := json.Marshal(builds)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cfg, err := ParseConfig(string(encoded))
	if err != nil {
		t.Fatalf("ParseConfig rejected the chart defaults: %v", err)
	}
	buildkit, ok := builds["buildkitImage"].(map[string]any)
	if !ok {
		t.Fatalf("chart buildkitImage = %v", builds["buildkitImage"])
	}
	repository, repositoryOK := buildkit["repository"].(string)
	tag, tagOK := buildkit["tag"].(string)
	if !repositoryOK || !tagOK {
		t.Fatalf("chart buildkitImage = %v", buildkit)
	}
	wantBuildkit := repository + ":" + tag
	if got := cfg.BuildkitImage.Reference(); got != wantBuildkit {
		t.Errorf("buildkit image = %q, want the chart's %q", got, wantBuildkit)
	}
	if cfg.Namespace != builds["namespace"] {
		t.Errorf("namespace = %q, want the chart's %v", cfg.Namespace, builds["namespace"])
	}
	if cfg.Resources.Build.Limits.Memory().IsZero() || cfg.Storage.Cache.IsZero() {
		t.Errorf("resources or storage missing: %+v %+v", cfg.Resources, cfg.Storage)
	}
}

func TestParseConfigReadsOverrides(t *testing.T) {
	toleration := map[string]any{"key": "builds", "operator": "Exists", "effect": "NoSchedule"}
	raw := chartConfig(t, map[string]any{
		"timeout":         "45m",
		"downloadTimeout": "5m",
		"pushSecretName":  "push",
		"tolerations":     []any{toleration},
	})
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Timeout.Duration != 45*time.Minute || cfg.DownloadTimeout.Duration != 5*time.Minute {
		t.Errorf("timeout = %s, download timeout = %s", cfg.Timeout.Duration, cfg.DownloadTimeout.Duration)
	}
	if cfg.PushSecretName != "push" {
		t.Errorf("push secret = %q", cfg.PushSecretName)
	}
	if len(cfg.Tolerations) != 1 || cfg.Tolerations[0].Effect != corev1.TaintEffectNoSchedule {
		t.Errorf("tolerations = %+v", cfg.Tolerations)
	}
}

func TestParseConfigRequiresTheChartSettings(t *testing.T) {
	if _, err := ParseConfig(""); err == nil {
		t.Fatal("ParseConfig accepted an empty configuration")
	}
	if _, err := ParseConfig(`{"namespace":"loco-builds"}`); err == nil {
		t.Fatal("ParseConfig accepted a configuration without images or limits")
	}
}

func TestParseConfigRejectsInvalid(t *testing.T) {
	cases := map[string]map[string]any{
		"short ttl":         {"ttlAfterFinished": "30s"},
		"no timeout":        {"timeout": "0s"},
		"no download limit": {"downloadTimeout": "0s"},
		"no builder image":  {"builderImage": map[string]any{}},
		"no source limit":   {"maxSourceSize": "0"},
		"no context files":  {"maxContextFiles": 0},
		"small workspace":   {"maxContextSize": "4Gi", "storage": map[string]any{"workspace": "1Gi"}},
		"bad registry cidr": {"privateEgressCIDRs": []any{"10.0.0.1"}},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			raw := chartConfig(t, overrides)
			if _, err := ParseConfig(raw); err == nil {
				t.Errorf("ParseConfig(%s) succeeded", raw)
			}
		})
	}
	if _, err := ParseConfig(`builds`); err == nil {
		t.Error("ParseConfig accepted invalid JSON")
	}
}
