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

// AllowedSchemaVersions defines supported config versions
var AllowedSchemaVersions = []string{
	"0.1",
}

// BannedSubdomains are reserved subdomains that cannot be used
var BannedSubdomains = []string{
	"api", "admin", "dashboard", "console",
	"login", "auth", "user", "users", "support", "help", "loco", "monitoring",
	"metrics", "stats", "status", "health", "system", "service", "services",
	"config", "configuration", "settings", "setup", "install", "uninstall",
}

// Default provides sensible defaults for a new AppConfig
var Default = &AppConfig{
	Metadata: Metadata{
		ConfigVersion: "0.1",
		Description:   "Default Loco app configuration",
		Name:          "<ENTER_APP_NAME>",
		Type:          "SERVICE",
	},
	Build: Build{
		DockerfilePath: "Dockerfile",
		Type:           "docker",
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
	DomainConfig: DomainConfig{
		Type:     "platform",
		Hostname: "loco.deploy-app.com",
	},
}

// FillSensibleDefaults applies defaults to a config where values are not set
func FillSensibleDefaults(cfg *AppConfig) {
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

// Validate ensures the AppConfig is valid according to the schema
func Validate(cfg *AppConfig) error {
	if cfg.Metadata.ConfigVersion == "" {
		return fmt.Errorf("metadata.configVersion must be set")
	}
	if !isAllowedSchemaVersion(cfg.Metadata.ConfigVersion) {
		return fmt.Errorf("metadata.configVersion %q is not supported. allowed versions: %v", cfg.Metadata.ConfigVersion, AllowedSchemaVersions)
	}

	if cfg.Metadata.Name == "" {
		return fmt.Errorf("metadata.name must be set")
	}

	if cfg.DomainConfig.Hostname == "" {
		return fmt.Errorf("domainConfig.hostname must be set (e.g., 'myapp.deploy-app.com')")
	}

	if cfg.DomainConfig.Type != "" && cfg.DomainConfig.Type != "platform" && cfg.DomainConfig.Type != "custom" {
		return fmt.Errorf("domainConfig.type must be 'platform' or 'custom', got %q", cfg.DomainConfig.Type)
	}

	if cfg.DomainConfig.Type == "" {
		cfg.DomainConfig.Type = "platform"
	}

	if cfg.Routing.Port <= 1023 || cfg.Routing.Port > 65535 {
		return fmt.Errorf("routing.port must be between 1024 and 65535, got %d", cfg.Routing.Port)
	}

	if cfg.Routing.PathPrefix == "" {
		cfg.Routing.PathPrefix = "/"
	} else if !strings.HasPrefix(cfg.Routing.PathPrefix, "/") {
		return fmt.Errorf("routing.pathPrefix must start with '/'")
	}

	if cfg.Routing.IdleTimeout < 0 {
		return fmt.Errorf("routing.idleTimeout cannot be negative")
	}

	if cfg.Build.DockerfilePath == "" {
		cfg.Build.DockerfilePath = "Dockerfile"
	}

	if cfg.Build.Type == "" {
		cfg.Build.Type = "docker"
	}
	if cfg.Build.Type != "docker" {
		return fmt.Errorf("build.type %q is not supported. only 'docker' is allowed", cfg.Build.Type)
	}

	if len(cfg.RegionConfig) == 0 {
		return fmt.Errorf("regionConfig must have at least one region configured")
	}

	for region, resources := range cfg.RegionConfig {
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

		if resources.ScalersEnabled {
			if resources.ScalersCPUTarget == 0 && resources.ScalersMemTarget == 0 {
				return fmt.Errorf("regionConfig.%s: when scalers_enabled=true, either scalers_cpu_target or scalers_memory_target must be provided (non-zero)", region)
			}
			if resources.ScalersCPUTarget != 0 && resources.ScalersMemTarget != 0 {
				return fmt.Errorf("regionConfig.%s: only one of scalers_cpu_target or scalers_memory_target should be provided", region)
			}
			if resources.ScalersCPUTarget != 0 && (resources.ScalersCPUTarget < 1 || resources.ScalersCPUTarget > 100) {
				return fmt.Errorf("regionConfig.%s.scalers_cpu_target must be between 1 and 100 (0 means disabled)", region)
			}
			if resources.ScalersMemTarget != 0 && (resources.ScalersMemTarget < 1 || resources.ScalersMemTarget > 100) {
				return fmt.Errorf("regionConfig.%s.scalers_memory_target must be between 1 and 100 (0 means disabled)", region)
			}
		}
	}

	// --- Health ---
	if cfg.Health.Path == "" {
		return fmt.Errorf("health.path must be provided")
	}
	if !strings.HasPrefix(cfg.Health.Path, "/") {
		return fmt.Errorf("health.path must start with '/'")
	}
	if cfg.Health.Interval <= 0 {
		return fmt.Errorf("health.interval must be greater than 0")
	}
	if cfg.Health.Timeout <= 0 {
		return fmt.Errorf("health.timeout must be greater than 0")
	}
	if cfg.Health.StartupGracePeriod < 0 {
		return fmt.Errorf("health.startupGracePeriod cannot be negative")
	}
	if cfg.Health.StartupGracePeriod > 300 {
		return fmt.Errorf("health.startupGracePeriod cannot exceed 300 seconds (5 minutes)")
	}
	if cfg.Health.FailThreshold < 0 {
		return fmt.Errorf("health.failThreshold cannot be negative")
	}

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
			return fmt.Errorf("obs.metrics.path must start with '/'")
		}
		if cfg.Obs.Metrics.Port <= 0 {
			cfg.Obs.Metrics.Port = 9090
		}
		if cfg.Obs.Metrics.Port <= 1023 || cfg.Obs.Metrics.Port > 65535 {
			return fmt.Errorf("obs.metrics.port must be between 1024 and 65535")
		}
	}

	if cfg.Obs.Tracing.Enabled {
		if cfg.Obs.Tracing.SampleRate < 0 || cfg.Obs.Tracing.SampleRate > 1 {
			return fmt.Errorf("obs.tracing.sampleRate must be between 0.0 and 1.0")
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
// e.g., "myapp.deploy-app.com" -> "myapp"
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

// isBannedSubdomain checks if a subdomain is in the banned list
func isBannedSubdomain(subdomain string) bool {
	for _, banned := range BannedSubdomains {
		if strings.EqualFold(subdomain, banned) {
			return true
		}
	}
	return false
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
func ResolveConfigPaths(cfg *AppConfig, cfgPath string) error {
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
	Config      *AppConfig
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
			return nil, fmt.Errorf("loco.toml not found. Please run 'loco init' to create the file or run the cmd with --config to specify a custom path")
		}

		return nil, fmt.Errorf("failed to open loco.toml: %w", err)
	}
	defer file.Close()

	var cfg AppConfig
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

// Create writes a AppConfig to a loco.toml file at the specified path
func Create(cfg *AppConfig, outputPath string) error {
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
func CreateDefault(appName string) error {
	cfg := *Default // Copy the default config
	cfg.Metadata.Name = appName
	cfg.DomainConfig.Hostname = appName + ".deploy-app.com"
	cfg.RegionConfig = map[string]Resources{
		"us-east-1": {
			CPU:         "100m",
			Memory:      "256Mi",
			ReplicasMin: 1,
			ReplicasMax: 1,
		},
	}

	return Create(&cfg, "loco.toml")
}
