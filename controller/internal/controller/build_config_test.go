package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const chartBuildConfig = `{"builderImage":{"pullPolicy":"Always",` +
	`"repository":"ghcr.io/team-loco/loco-builder","tag":"latest"},` +
	`"buildkitImage":{"pullPolicy":"IfNotPresent","repository":"moby/buildkit","tag":"v0.33.1-rootless"},` +
	`"insecureRegistry":false,"maxContextFiles":200000,"maxContextSize":"1Gi","maxImageSize":"1Gi",` +
	`"maxSourceSize":"200Mi","namespace":"loco-builds","nodeSelector":{},"pushSecretName":"push",` +
	`"privateEgressCIDRs":["10.0.0.5/32"],"resources":{"build":{"limits":{"cpu":"2","memory":"4Gi"},` +
	`"requests":{"cpu":"500m","memory":"512Mi"}},"fetch":{"limits":{"cpu":"1","memory":"256Mi"},` +
	`"requests":{"cpu":"100m","memory":"64Mi"}},"push":{"limits":{"cpu":"1","memory":"512Mi"},` +
	`"requests":{"cpu":"100m","memory":"64Mi"}}},"runtimeClassName":"","seccompLocalhostProfile":"",` +
	`"storage":{"buildkit":"10Gi","cache":"5Gi","out":"10Gi","workspace":"2Gi"},"timeout":"45m","downloadTimeout":"5m",` +
	`"tolerations":[{"key":"builds","operator":"Exists","effect":"NoSchedule"}],"ttlAfterFinished":"10m"}`

func TestParseBuildConfigReadsChartValues(t *testing.T) {
	cfg, err := ParseBuildConfig(chartBuildConfig)
	if err != nil {
		t.Fatalf("ParseBuildConfig: %v", err)
	}
	if cfg.Timeout.Duration != 45*time.Minute || cfg.DownloadTimeout.Duration != 5*time.Minute {
		t.Errorf("timeout = %s, download timeout = %s", cfg.Timeout.Duration, cfg.DownloadTimeout.Duration)
	}
	if cfg.PushSecretName != "push" || cfg.BuilderImage.Reference() != "ghcr.io/team-loco/loco-builder:latest" {
		t.Errorf("push secret %q, builder image %q", cfg.PushSecretName, cfg.BuilderImage.Reference())
	}
	if cfg.BuildkitImage.PullPolicy != corev1.PullIfNotPresent {
		t.Errorf("buildkit pull policy = %q", cfg.BuildkitImage.PullPolicy)
	}
	if cfg.MaxImageSize.Value() != 1<<30 || cfg.Storage.Cache.Value() != 5<<30 {
		t.Errorf("max image %d, cache %d", cfg.MaxImageSize.Value(), cfg.Storage.Cache.Value())
	}
	if len(cfg.Tolerations) != 1 || cfg.Tolerations[0].Effect != corev1.TaintEffectNoSchedule {
		t.Errorf("tolerations = %+v", cfg.Tolerations)
	}
	memory := cfg.Resources.Build.Limits[corev1.ResourceMemory]
	if memory.String() != "4Gi" {
		t.Errorf("build memory limit = %s", memory.String())
	}
}

func TestParseBuildConfigDefaults(t *testing.T) {
	cfg, err := ParseBuildConfig("")
	if err != nil {
		t.Fatalf("ParseBuildConfig: %v", err)
	}
	if cfg.Namespace != defaultBuildNamespace || cfg.Timeout.Duration != defaultBuildTimeout {
		t.Errorf("defaults = %+v", cfg)
	}
}

func TestParseBuildConfigRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"short ttl":         `{"ttlAfterFinished":"30s"}`,
		"no timeout":        `{"timeout":"0s"}`,
		"no download limit": `{"downloadTimeout":"0s"}`,
		"small workspace":   `{"maxContextSize":"4Gi","storage":{"workspace":"1Gi"}}`,
		"bad registry cidr": `{"privateEgressCIDRs":["10.0.0.1"]}`,
		"not json":          `builds`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBuildConfig(raw); err == nil {
				t.Errorf("ParseBuildConfig(%s) succeeded", raw)
			}
		})
	}
}
