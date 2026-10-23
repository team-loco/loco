package builds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	EnvConfig   = "LOCO_BUILD_CONFIG"
	minBuildTTL = 2 * time.Minute
)

var (
	errNoConfig     = errors.New("the build settings are missing; the loco-operator chart sets them")
	errNoNamespace  = errors.New("namespace is required")
	errNoImages     = errors.New("builderImage and buildkitImage are required")
	errNoTimeout    = errors.New("timeout must be positive")
	errNoDownload   = errors.New("downloadTimeout must be positive")
	errNoSizeLimits = errors.New(
		"maxSourceSize, maxContextSize, maxContextFiles and maxImageSize must be positive",
	)
	errWorkspaceTooSmall = errors.New("storage.workspace must be at least maxContextSize")
	errOutTooSmall       = errors.New("storage.out must be at least maxImageSize")
	errNoStorage         = errors.New("storage.cache and storage.buildkit must be positive")
	errEgressHostAddress = errors.New("is an IP address; list the destination by hostname")
	errEgressHostPort    = errors.New("needs a port from 1 to 65535")
)

type Image struct {
	Repository string            `json:"repository"`
	Tag        string            `json:"tag"`
	PullPolicy corev1.PullPolicy `json:"pullPolicy"`
}

func (i Image) Reference() string {
	if i.Tag == "" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

type Resources struct {
	Fetch corev1.ResourceRequirements `json:"fetch"`
	Build corev1.ResourceRequirements `json:"build"`
	Push  corev1.ResourceRequirements `json:"push"`
}

type Storage struct {
	Workspace resource.Quantity `json:"workspace"`
	Cache     resource.Quantity `json:"cache"`
	Out       resource.Quantity `json:"out"`
	Buildkit  resource.Quantity `json:"buildkit"`
}

type EgressHost struct {
	Name string
	Port uint16
}

func (h *EgressHost) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("privateEgressHosts: %w", err)
	}
	name, port, err := net.SplitHostPort(raw)
	if err != nil {
		return fmt.Errorf("privateEgressHosts: %w", err)
	}
	if _, addrErr := netip.ParseAddr(name); addrErr == nil {
		return fmt.Errorf("privateEgressHosts: %q %w", raw, errEgressHostAddress)
	}
	if problems := validation.IsDNS1123Subdomain(name); len(problems) > 0 {
		return fmt.Errorf("privateEgressHosts: %q: %s", raw, strings.Join(problems, "; "))
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return fmt.Errorf("privateEgressHosts: %q %w", raw, errEgressHostPort)
	}
	h.Name = name
	h.Port = uint16(number)
	return nil
}

type Config struct {
	Namespace               string              `json:"namespace"`
	PushSecretName          string              `json:"pushSecretName"`
	BuilderImage            Image               `json:"builderImage"`
	BuildkitImage           Image               `json:"buildkitImage"`
	Timeout                 metav1.Duration     `json:"timeout"`
	TTLAfterFinished        metav1.Duration     `json:"ttlAfterFinished"`
	DownloadTimeout         metav1.Duration     `json:"downloadTimeout"`
	MaxSourceSize           resource.Quantity   `json:"maxSourceSize"`
	MaxContextSize          resource.Quantity   `json:"maxContextSize"`
	MaxContextFiles         int                 `json:"maxContextFiles"`
	MaxImageSize            resource.Quantity   `json:"maxImageSize"`
	InsecureRegistry        bool                `json:"insecureRegistry"`
	PrivateEgressHosts      []EgressHost        `json:"privateEgressHosts"`
	SeccompLocalhostProfile string              `json:"seccompLocalhostProfile"`
	RuntimeClassName        string              `json:"runtimeClassName"`
	NodeSelector            map[string]string   `json:"nodeSelector"`
	Tolerations             []corev1.Toleration `json:"tolerations"`
	Resources               Resources           `json:"resources"`
	Storage                 Storage             `json:"storage"`
}

func ParseConfig(raw string) (Config, error) {
	if raw == "" {
		return Config{}, fmt.Errorf("%s: %w", EnvConfig, errNoConfig)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", EnvConfig, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", EnvConfig, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Namespace == "" {
		return errNoNamespace
	}
	if c.BuilderImage.Repository == "" || c.BuildkitImage.Repository == "" {
		return errNoImages
	}
	if c.Timeout.Duration <= 0 {
		return errNoTimeout
	}
	if c.DownloadTimeout.Duration <= 0 {
		return errNoDownload
	}
	if c.TTLAfterFinished.Duration < minBuildTTL {
		return fmt.Errorf("ttlAfterFinished must be at least %s so build logs can be collected", minBuildTTL)
	}
	sizes := []resource.Quantity{c.MaxSourceSize, c.MaxContextSize, c.MaxImageSize}
	for _, size := range sizes {
		if size.Sign() <= 0 {
			return errNoSizeLimits
		}
	}
	if c.MaxContextFiles <= 0 {
		return errNoSizeLimits
	}
	if c.Storage.Workspace.Cmp(c.MaxContextSize) < 0 {
		return errWorkspaceTooSmall
	}
	if c.Storage.Out.Cmp(c.MaxImageSize) < 0 {
		return errOutTooSmall
	}
	if c.Storage.Cache.Sign() <= 0 || c.Storage.Buildkit.Sign() <= 0 {
		return errNoStorage
	}
	return nil
}
