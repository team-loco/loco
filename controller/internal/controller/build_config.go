package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	EnvBuildConfig = "LOCO_BUILD_CONFIG"

	defaultBuildNamespace   = "loco-builds"
	defaultBuildTimeout     = 30 * time.Minute
	defaultBuildTTL         = 10 * time.Minute
	defaultDownloadTimeout  = 10 * time.Minute
	minBuildTTL             = 2 * time.Minute
	defaultMaxSourceSize    = "200Mi"
	defaultMaxContextSize   = "1Gi"
	defaultMaxContextFiles  = 200000
	defaultMaxImageSize     = "1Gi"
	defaultWorkspaceStorage = "2Gi"
	defaultCacheStorage     = "5Gi"
	defaultOutStorage       = "10Gi"
	defaultBuildkitStorage  = "10Gi"
)

type BuildImage struct {
	Repository string            `json:"repository"`
	Tag        string            `json:"tag"`
	PullPolicy corev1.PullPolicy `json:"pullPolicy"`
}

func (i BuildImage) Reference() string {
	if i.Tag == "" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

type BuildResources struct {
	Fetch corev1.ResourceRequirements `json:"fetch"`
	Build corev1.ResourceRequirements `json:"build"`
	Push  corev1.ResourceRequirements `json:"push"`
}

type BuildStorage struct {
	Workspace resource.Quantity `json:"workspace"`
	Cache     resource.Quantity `json:"cache"`
	Out       resource.Quantity `json:"out"`
	Buildkit  resource.Quantity `json:"buildkit"`
}

type BuildConfig struct {
	Namespace               string              `json:"namespace"`
	PushSecretName          string              `json:"pushSecretName"`
	BuilderImage            BuildImage          `json:"builderImage"`
	BuildkitImage           BuildImage          `json:"buildkitImage"`
	Timeout                 metav1.Duration     `json:"timeout"`
	TTLAfterFinished        metav1.Duration     `json:"ttlAfterFinished"`
	DownloadTimeout         metav1.Duration     `json:"downloadTimeout"`
	MaxSourceSize           resource.Quantity   `json:"maxSourceSize"`
	MaxContextSize          resource.Quantity   `json:"maxContextSize"`
	MaxContextFiles         int                 `json:"maxContextFiles"`
	MaxImageSize            resource.Quantity   `json:"maxImageSize"`
	InsecureRegistry        bool                `json:"insecureRegistry"`
	PrivateEgressCIDRs      []string            `json:"privateEgressCIDRs"`
	SeccompLocalhostProfile string              `json:"seccompLocalhostProfile"`
	RuntimeClassName        string              `json:"runtimeClassName"`
	NodeSelector            map[string]string   `json:"nodeSelector"`
	Tolerations             []corev1.Toleration `json:"tolerations"`
	Resources               BuildResources      `json:"resources"`
	Storage                 BuildStorage        `json:"storage"`
}

func defaultResources(cpuRequest, memoryRequest, cpuLimit, memoryLimit string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuRequest),
			corev1.ResourceMemory: resource.MustParse(memoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuLimit),
			corev1.ResourceMemory: resource.MustParse(memoryLimit),
		},
	}
}

func DefaultBuildConfig() BuildConfig {
	return BuildConfig{
		Namespace:        defaultBuildNamespace,
		BuilderImage:     BuildImage{Repository: "ghcr.io/team-loco/loco-builder", Tag: "latest"},
		BuildkitImage:    BuildImage{Repository: "moby/buildkit", Tag: "v0.33.1-rootless"},
		Timeout:          metav1.Duration{Duration: defaultBuildTimeout},
		TTLAfterFinished: metav1.Duration{Duration: defaultBuildTTL},
		DownloadTimeout:  metav1.Duration{Duration: defaultDownloadTimeout},
		MaxSourceSize:    resource.MustParse(defaultMaxSourceSize),
		MaxContextSize:   resource.MustParse(defaultMaxContextSize),
		MaxContextFiles:  defaultMaxContextFiles,
		MaxImageSize:     resource.MustParse(defaultMaxImageSize),
		Resources: BuildResources{
			Fetch: defaultResources("100m", "64Mi", "1", "256Mi"),
			Build: defaultResources("500m", "512Mi", "2", "4Gi"),
			Push:  defaultResources("100m", "64Mi", "1", "512Mi"),
		},
		Storage: BuildStorage{
			Workspace: resource.MustParse(defaultWorkspaceStorage),
			Cache:     resource.MustParse(defaultCacheStorage),
			Out:       resource.MustParse(defaultOutStorage),
			Buildkit:  resource.MustParse(defaultBuildkitStorage),
		},
	}
}

func ParseBuildConfig(raw string) (BuildConfig, error) {
	cfg := DefaultBuildConfig()
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return BuildConfig{}, fmt.Errorf("parse %s: %w", EnvBuildConfig, err)
		}
	}
	if err := cfg.validate(); err != nil {
		return BuildConfig{}, fmt.Errorf("invalid %s: %w", EnvBuildConfig, err)
	}
	return cfg, nil
}

func (c *BuildConfig) validate() error {
	if c.Namespace == "" {
		return errors.New("namespace is required")
	}
	if c.BuilderImage.Repository == "" || c.BuildkitImage.Repository == "" {
		return errors.New("builderImage and buildkitImage are required")
	}
	if c.Timeout.Duration <= 0 {
		return errors.New("timeout must be positive")
	}
	if c.DownloadTimeout.Duration <= 0 {
		return errors.New("downloadTimeout must be positive")
	}
	if c.TTLAfterFinished.Duration < minBuildTTL {
		return fmt.Errorf("ttlAfterFinished must be at least %s so build logs can be collected", minBuildTTL)
	}
	if c.Storage.Workspace.Cmp(c.MaxContextSize) < 0 {
		return errors.New("storage.workspace must be at least maxContextSize")
	}
	if c.Storage.Out.Cmp(c.MaxImageSize) < 0 {
		return errors.New("storage.out must be at least maxImageSize")
	}
	for _, cidr := range c.PrivateEgressCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("privateEgressCIDRs: %w", err)
		}
	}
	return nil
}
