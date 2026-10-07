package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

var (
	errConfigVersionMissing       = errors.New("metadata.configVersion must be set")
	errNameMissing                = errors.New("metadata.name must be set")
	errHostnameMissing            = errors.New("domainConfig.hostname must be set (e.g., 'myapp.onloco.app')")
	errPathPrefixNoSlash          = errors.New("routing.pathPrefix must start with '/'")
	errNegativeIdleTimeout        = errors.New("routing.idleTimeout cannot be negative")
	errNoRegions                  = errors.New("regionConfig must have at least one region configured")
	errRegionNotConfigured        = errors.New("metadata.region must be set and match one of the configured regions")
	errHealthPathMissing          = errors.New("health.path must be provided")
	errHealthPathNoSlash          = errors.New("health.path must start with '/'")
	errHealthIntervalNotPositive  = errors.New("health.interval must be greater than 0")
	errHealthTimeoutNotPositive   = errors.New("health.timeout must be greater than 0")
	errNegativeStartupGracePeriod = errors.New("health.startupGracePeriod cannot be negative")
	errStartupGracePeriodTooLong  = errors.New("health.startupGracePeriod cannot exceed 300 seconds (5 minutes)")
	errNegativeFailThreshold      = errors.New("health.failThreshold cannot be negative")
	errMetricsPathNoSlash         = errors.New("obs.metrics.path must start with '/'")
	errMetricsPortOutOfRange      = errors.New("obs.metrics.port must be between 1024 and 65535")
	errSampleRateOutOfRange       = errors.New("obs.tracing.sampleRate must be between 0.0 and 1.0")
	errConfigNotFound             = errors.New("loco.toml not found. Please run 'loco init' to create the file " +
		"or run the cmd with --config to specify a custom path")
)

const DefaultAppDomain = "onloco.app"

const (
	defaultRegion      = "us-east-1"
	buildTypeDocker    = "docker"
	domainTypePlatform = "platform"
)

// AllowedSchemaVersions defines supported config versions
var AllowedSchemaVersions = []string{
	"0.1",
}

// Default provides sensible defaults for a new LocoConfig
var Default = &LocoConfig{
	Metadata: Metadata{
		ConfigVersion: "0.1",
		Description:   "Default Loco app configuration",
		Name:          "<ENTER_APP_NAME>",
		Type:          "SERVICE",
		Region:        defaultRegion,
	},
	Build: Build{
		DockerfilePath: "Dockerfile",
		Type:           buildTypeDocker,
	},
	Routing: Routing{
		IdleTimeout: 60,
		PathPrefix:  "/",
		Port:        8000,
	},
	Health: Health{
		Interval:           30,
		Path:               "/health",
		StartupGracePeriod: 0,
		Timeout:            5,
		FailThreshold:      3,
	},
	Env: Env{
		Variables: map[string]string{},
	},
	Obs: Obs{
		Logging: Logging{
			Enabled:         true,
			RetentionPeriod: "7d",
			Structured:      false,
		},
		Metrics: Metrics{
			Enabled: false,
			Path:    "/metrics",
			Port:    9090,
		},
		Tracing: Tracing{
			Enabled:    false,
			SampleRate: 0.1,
			Tags:       map[string]string{},
		},
	},
}

// FillSensibleDefaults applies defaults to a config where values are not set
func FillSensibleDefaults(cfg *LocoConfig) {
	if cfg.Build.DockerfilePath == "" {
		cfg.Build.DockerfilePath = Default.Build.DockerfilePath
	}
	if cfg.Build.Type == "" {
		cfg.Build.Type = Default.Build.Type
	}

	if cfg.Routing.PathPrefix == "" {
		cfg.Routing.PathPrefix = Default.Routing.PathPrefix
	}
	if cfg.Routing.IdleTimeout == 0 {
		cfg.Routing.IdleTimeout = Default.Routing.IdleTimeout
	}

	if cfg.Health.Timeout == 0 {
		cfg.Health.Timeout = Default.Health.Timeout
	}
	if cfg.Health.FailThreshold == 0 {
		cfg.Health.FailThreshold = Default.Health.FailThreshold
	}

	if cfg.Obs.Logging.RetentionPeriod == "" {
		cfg.Obs.Logging.RetentionPeriod = Default.Obs.Logging.RetentionPeriod
	}
	if cfg.Obs.Metrics.Path == "" {
		cfg.Obs.Metrics.Path = Default.Obs.Metrics.Path
	}
	if cfg.Obs.Metrics.Port == 0 {
		cfg.Obs.Metrics.Port = Default.Obs.Metrics.Port
	}
	if cfg.Obs.Tracing.SampleRate == 0 {
		cfg.Obs.Tracing.SampleRate = Default.Obs.Tracing.SampleRate
	}
}

// Validate ensures the LocoConfig is valid according to the schema
func Validate(cfg *LocoConfig) error {
	validators := []func(*LocoConfig) error{
		validateMetadata,
		validateDomain,
		validateRouting,
		validateBuild,
		validateRegions,
		validateHealth,
		validateObs,
	}
	for _, validate := range validators {
		if err := validate(cfg); err != nil {
			return err
		}
	}

	return nil
}

func validateMetadata(cfg *LocoConfig) error {
	if cfg.Metadata.ConfigVersion == "" {
		return errConfigVersionMissing
	}
	if !isAllowedSchemaVersion(cfg.Metadata.ConfigVersion) {
		return fmt.Errorf(
			"metadata.configVersion %q is not supported. allowed versions: %v",
			cfg.Metadata.ConfigVersion,
			AllowedSchemaVersions,
		)
	}

	if cfg.Metadata.Name == "" {
		return errNameMissing
	}

	return nil
}

func validateDomain(cfg *LocoConfig) error {
	if cfg.DomainConfig != nil {
		if cfg.DomainConfig.Hostname == "" {
			return errHostnameMissing
		}
		if cfg.DomainConfig.Type != "" && cfg.DomainConfig.Type != domainTypePlatform &&
			cfg.DomainConfig.Type != "custom" {
			return fmt.Errorf("domainConfig.type must be 'platform' or 'custom', got %q", cfg.DomainConfig.Type)
		}
		if cfg.DomainConfig.Type == "" {
			cfg.DomainConfig.Type = domainTypePlatform
		}
	}

	return nil
}

func validateRouting(cfg *LocoConfig) error {
	if cfg.Routing.Port <= 1023 || cfg.Routing.Port > 65535 {
		return fmt.Errorf("routing.port must be between 1024 and 65535, got %d", cfg.Routing.Port)
	}

	if cfg.Routing.PathPrefix == "" {
		cfg.Routing.PathPrefix = "/"
	} else if !strings.HasPrefix(cfg.Routing.PathPrefix, "/") {
		return errPathPrefixNoSlash
	}

	if cfg.Routing.IdleTimeout < 0 {
		return errNegativeIdleTimeout
	}

	return nil
}

func validateBuild(cfg *LocoConfig) error {
	if cfg.Build.DockerfilePath == "" {
		cfg.Build.DockerfilePath = "Dockerfile"
	}

	if cfg.Build.Type == "" {
		cfg.Build.Type = buildTypeDocker
	}
	if cfg.Build.Type != buildTypeDocker {
		return fmt.Errorf("build.type %q is not supported. only 'docker' is allowed", cfg.Build.Type)
	}

	return nil
}

func validateRegions(cfg *LocoConfig) error {
	if len(cfg.RegionConfig) == 0 {
		return errNoRegions
	}

	if cfg.Metadata.Region == "" {
		return errRegionNotConfigured
	}

	if _, exists := cfg.RegionConfig[cfg.Metadata.Region]; !exists {
		return fmt.Errorf("metadata.region %q is not configured in regionConfig", cfg.Metadata.Region)
	}

	for region, resources := range cfg.RegionConfig {
		if err := validateRegionResources(region, resources); err != nil {
			return err
		}
	}

	return nil
}

func validateRegionResources(region string, resources Resources) error {
	if resources.CPU == "" {
		return fmt.Errorf("regionConfig.%s.cpu must be set (e.g. '100m')", region)
	}
	if resources.Memory == "" {
		return fmt.Errorf("regionConfig.%s.memory must be set (e.g. '512Mi')", region)
	}

	if resources.ReplicasMin <= 0 {
		return fmt.Errorf("regionConfig.%s.replicas_min must be greater than 0", region)
	}
	if resources.ReplicasMax <= 0 {
		return fmt.Errorf("regionConfig.%s.replicas_max must be greater than 0", region)
	}
	if resources.ReplicasMax < resources.ReplicasMin {
		return fmt.Errorf("regionConfig.%s.replicas_max must be greater than or equal to replicas_min", region)
	}
	if resources.ReplicasMax > 3 {
		return fmt.Errorf("regionConfig.%s.replicas_max cannot exceed 3 replicas", region)
	}

	if resources.EnableAutoScaling {
		if resources.CPUTarget == 0 && resources.ScalersMemTarget == 0 {
			return fmt.Errorf(
				"regionConfig.%s: when scalers_enabled=true, either scalers_cpu_target or "+
					"scalers_memory_target must be provided (non-zero)",
				region,
			)
		}
		if resources.CPUTarget != 0 && resources.ScalersMemTarget != 0 {
			return fmt.Errorf(
				"regionConfig.%s: only one of scalers_cpu_target or scalers_memory_target should be provided",
				region,
			)
		}
		if resources.CPUTarget != 0 && (resources.CPUTarget < 1 || resources.CPUTarget > 100) {
			return fmt.Errorf(
				"regionConfig.%s.scalers_cpu_target must be between 1 and 100 (0 means disabled)",
				region,
			)
		}
		if resources.ScalersMemTarget != 0 && (resources.ScalersMemTarget < 1 || resources.ScalersMemTarget > 100) {
			return fmt.Errorf(
				"regionConfig.%s.scalers_memory_target must be between 1 and 100 (0 means disabled)",
				region,
			)
		}
	}

	return nil
}

func validateHealth(cfg *LocoConfig) error {
	if cfg.Health.Path == "" {
		return errHealthPathMissing
	}
	if !strings.HasPrefix(cfg.Health.Path, "/") {
		return errHealthPathNoSlash
	}
	if cfg.Health.Interval <= 0 {
		return errHealthIntervalNotPositive
	}
	if cfg.Health.Timeout <= 0 {
		return errHealthTimeoutNotPositive
	}
	if cfg.Health.StartupGracePeriod < 0 {
		return errNegativeStartupGracePeriod
	}
	if cfg.Health.StartupGracePeriod > 300 {
		return errStartupGracePeriodTooLong
	}
	if cfg.Health.FailThreshold < 0 {
		return errNegativeFailThreshold
	}

	return nil
}

func validateObs(cfg *LocoConfig) error {
	if cfg.Obs.Logging.Enabled {
		if cfg.Obs.Logging.RetentionPeriod == "" {
			cfg.Obs.Logging.RetentionPeriod = "7d"
		}
		duration, err := parseRetention(cfg.Obs.Logging.RetentionPeriod)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid obs.logging.retentionPeriod: %q", cfg.Obs.Logging.RetentionPeriod)
		}
	}

	if cfg.Obs.Metrics.Enabled {
		if cfg.Obs.Metrics.Path == "" {
			cfg.Obs.Metrics.Path = "/metrics"
		}
		if !strings.HasPrefix(cfg.Obs.Metrics.Path, "/") {
			return errMetricsPathNoSlash
		}
		if cfg.Obs.Metrics.Port <= 0 {
			cfg.Obs.Metrics.Port = 9090
		}
		if cfg.Obs.Metrics.Port <= 1023 || cfg.Obs.Metrics.Port > 65535 {
			return errMetricsPortOutOfRange
		}
	}

	if cfg.Obs.Tracing.Enabled {
		if cfg.Obs.Tracing.SampleRate < 0 || cfg.Obs.Tracing.SampleRate > 1 {
			return errSampleRateOutOfRange
		}
	}

	return nil
}

// parseRetention parses retention period strings like "7d" or "24h"
func parseRetention(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		daysStr := strings.TrimSuffix(value, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil {
			return 0, err
		}
		return time.Hour * 24 * time.Duration(days), nil
	}
	return time.ParseDuration(value)
}

// ExtractSubdomainFromHostname extracts the leftmost label from a hostname
// e.g., "myapp.onloco.app" -> "myapp"
func ExtractSubdomainFromHostname(hostname string) string {
	if hostname == "" {
		return ""
	}
	parts := strings.Split(hostname, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return hostname
}

// isAllowedSchemaVersion checks if a schema version is in the allowed list
func isAllowedSchemaVersion(version string) bool {
	return slices.Contains(AllowedSchemaVersions, version)
}

// resolvePath converts relative paths to absolute based on a base directory
func resolvePath(path, baseDir string) string {
	if path == "" {
		return ""
	}

	if filepath.IsAbs(path) {
		return path
	}

	projectFolder := filepath.Dir(baseDir)
	return filepath.Join(projectFolder, path)
}

// ResolveConfigPaths resolves relative paths in the config to absolute paths
func ResolveConfigPaths(cfg *LocoConfig, cfgPath string) error {
	cfgPathAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to resolve config path: %w", err)
	}

	cfg.Build.DockerfilePath = resolvePath(cfg.Build.DockerfilePath, cfgPathAbs)
	cfg.Env.File = resolvePath(cfg.Env.File, cfgPathAbs)

	return nil
}

// LoadedConfig represents a loaded configuration with its project path
type LoadedConfig struct {
	Config      *LocoConfig
	ProjectPath string
}

// Load reads and parses a loco.toml file from the given path
func Load(cfgPath string) (*LoadedConfig, error) {
	cfgPathAbs, err := filepath.Abs(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}

	file, err := os.Open(cfgPathAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errConfigNotFound
		}

		return nil, fmt.Errorf("failed to open loco.toml: %w", err)
	}
	defer file.Close()

	var cfg LocoConfig
	decoder := toml.NewDecoder(file)
	if _, err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse loco.toml: %w", err)
	}

	if err := ResolveConfigPaths(&cfg, cfgPathAbs); err != nil {
		return nil, err
	}

	return &LoadedConfig{
		Config:      &cfg,
		ProjectPath: filepath.Dir(cfgPathAbs),
	}, nil
}

// Create writes a LocoConfig to a loco.toml file at the specified path
func Create(cfg *LocoConfig, outputPath string) error {
	var filePath string
	fileInfo, err := os.Stat(outputPath)
	if err == nil && fileInfo.IsDir() {
		filePath = filepath.Join(outputPath, "loco.toml")
	} else {
		filePath = outputPath
	}

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create loco.toml: %w", err)
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("failed to write loco.toml: %w", err)
	}

	return nil
}

// CreateDefault creates a new loco.toml file with sensible defaults
// appName is used as the application name and hostname
func CreateDefault(appName, appDomain string) error {
	cfg := *Default // Copy the default config
	cfg.Metadata.Name = appName
	cfg.Metadata.Region = defaultRegion
	cfg.DomainConfig = &DomainConfig{
		Type:     domainTypePlatform,
		Hostname: appName + "." + appDomain,
	}
	cfg.RegionConfig = map[string]Resources{
		defaultRegion: {
			CPU:         "100m",
			Memory:      "256Mi",
			ReplicasMin: 1,
			ReplicasMax: 1,
		},
	}

	return Create(&cfg, "loco.toml")
}
