package main

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/service"
)

var (
	errCacheAddrMissing   = errors.New("CACHE_ADDR required when CACHE_TYPE=valkey")
	errUnknownCacheType   = errors.New("unknown cache type")
	errInvalidSourceBytes = errors.New("LOCO_SOURCE_MAX_BYTES is not a positive integer")

	errInvalidForcePathStyle = errors.New("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE is not a boolean")
	errInvalidServiceDefault = errors.New("invalid service default")
	errRegistryAuthPartial   = errors.New("LOCO_REGISTRY_USERNAME and LOCO_REGISTRY_PASSWORD must be set together")
	errRegistryAuthNoHost    = errors.New("LOCO_REGISTRY_USERNAME is set without LOCO_REGISTRY_HOST")
	errInvalidRegistry       = errors.New("invalid registry configuration")
)

const (
	cacheTypeValkey       = "valkey"
	cacheTypeMemory       = "in-memory"
	defaultSourceMaxBytes = 200 * 1024 * 1024

	defaultServiceCPU         = "100m"
	defaultServiceMemory      = "256Mi"
	defaultServiceMinReplicas = 1
	defaultServiceMaxReplicas = 1
	defaultServicePathPrefix  = "/"
	defaultServiceIdleTimeout = 60

	defaultRegistryScheme            = "https://"
	defaultRegistryTimeout           = 30 * time.Second
	defaultImageRetention            = 5
	imageRetentionDisabled           = 0
	defaultImageSweepInterval        = 10 * time.Minute
	defaultImageSweepBuildBatch      = 100
	defaultImageSweepRepositoryBatch = 100
	defaultImageSweepTagBatch        = 100
	defaultImageSweepTagMinAge       = time.Hour

	defaultSourceSweepInterval   = 10 * time.Minute
	defaultSourceUploadGrace     = time.Hour
	defaultSourceOrphanMinAge    = 24 * time.Hour
	defaultSourceSweepBuildBatch = 100
	defaultSourceOrphanPageSize  = 1000
	defaultSourceOrphanMaxPages  = 5
)

type APIConfig struct {
	Env                   string // Environment (e.g., dev, prod)
	DatabaseURL           string // PostgreSQL connection string
	LogLevel              slog.Level
	Port                  string
	CacheType             string   // Cache backend type: "in-memory" or "valkey"
	CacheAddr             string   // Valkey address (when CacheType is "valkey")
	CORSAllowedOrigins    []string // CORS allowed origins (e.g., http://localhost:5173)
	DefaultPlatformDomain string   // Default platform domain returned by the config service
	MinCLIVersion         string
	PprofAddr             string
	GithubOAuth           service.GithubOAuthConfig
	SourceBucket          sourcebucket.Config
	SourceMaxBytes        int64
	RegistryHost          string
	RegistryPrefix        string
	Registry              registryclient.Config
	ImageSweep            service.ImageSweepConfig
	SourceSweep           service.SourceSweepConfig
	ServiceDefaults       servicedefaults.Defaults
}

func newAPIConfig() *APIConfig {
	cacheType, cacheAddr := newCacheConfig()
	registryHost := stringEnv("LOCO_REGISTRY_HOST", "")
	registryPrefix := stringEnv("LOCO_REGISTRY_PREFIX", "")
	sourceMaxBytes := positiveInt64Env("LOCO_SOURCE_MAX_BYTES", defaultSourceMaxBytes, errInvalidSourceBytes)
	sourceBucket := newSourceBucketConfig()
	serviceDefaults := newServiceDefaults()
	registry := newRegistryConfig(registryHost)
	imageSweep := newImageSweepConfig(registryHost, registryPrefix)
	sourceSweep := newSourceSweepConfig()
	githubOAuth := newGithubOAuthConfig()
	logLevel, ok := logLevelEnv("LOG_LEVEL")
	if !ok {
		logLevel = slog.LevelInfo
	}
	corsOrigins := listEnv("CORS_ALLOWED_ORIGINS")

	return &APIConfig{
		Env:                   stringEnv("APP_ENV", ""),
		DatabaseURL:           stringEnv("DATABASE_URL", ""),
		Port:                  stringEnv("APP_PORT", ""),
		LogLevel:              logLevel,
		CacheType:             cacheType,
		CacheAddr:             cacheAddr,
		CORSAllowedOrigins:    corsOrigins,
		DefaultPlatformDomain: stringEnv("DEFAULT_PLATFORM_DOMAIN", ""),
		MinCLIVersion:         stringEnv("MIN_CLI_VERSION", ""),
		PprofAddr:             stringEnv("PPROF_ADDR", ""),
		GithubOAuth:           githubOAuth,
		SourceBucket:          sourceBucket,
		SourceMaxBytes:        sourceMaxBytes,
		RegistryHost:          registryHost,
		RegistryPrefix:        registryPrefix,
		Registry:              registry,
		ImageSweep:            imageSweep,
		SourceSweep:           sourceSweep,
		ServiceDefaults:       serviceDefaults,
	}
}

func newCacheConfig() (string, string) {
	cacheType := stringEnv("CACHE_TYPE", cacheTypeMemory)
	cacheAddr := stringEnv("CACHE_ADDR", "")
	if cacheType != cacheTypeValkey && cacheType != cacheTypeMemory {
		panic(fmt.Errorf("%w: %q", errUnknownCacheType, cacheType))
	}
	if cacheType == cacheTypeValkey && cacheAddr == "" {
		panic(errCacheAddrMissing)
	}
	return cacheType, cacheAddr
}

func newSourceBucketConfig() sourcebucket.Config {
	forcePathStyle := boolEnv("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE", false, errInvalidForcePathStyle)
	cfg := sourcebucket.Config{
		Endpoint:        stringEnv("LOCO_SOURCE_BUCKET_ENDPOINT", ""),
		Bucket:          stringEnv("LOCO_SOURCE_BUCKET", ""),
		Region:          stringEnv("LOCO_SOURCE_BUCKET_REGION", ""),
		AccessKeyID:     stringEnv("LOCO_SOURCE_BUCKET_ACCESS_KEY_ID", ""),
		SecretAccessKey: stringEnv("LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY", ""),
		ForcePathStyle:  forcePathStyle,
	}
	if cfg.Bucket != "" {
		if err := cfg.Validate(); err != nil {
			panic(err)
		}
	}
	return cfg
}

func newServiceDefaults() servicedefaults.Defaults {
	defaults := servicedefaults.Defaults{
		CPU:         stringEnv("LOCO_DEFAULT_CPU", defaultServiceCPU),
		Memory:      stringEnv("LOCO_DEFAULT_MEMORY", defaultServiceMemory),
		MinReplicas: int32Env("LOCO_DEFAULT_MIN_REPLICAS", defaultServiceMinReplicas),
		MaxReplicas: int32Env("LOCO_DEFAULT_MAX_REPLICAS", defaultServiceMaxReplicas),
		PathPrefix:  stringEnv("LOCO_DEFAULT_PATH_PREFIX", defaultServicePathPrefix),
		IdleTimeout: int32Env("LOCO_DEFAULT_IDLE_TIMEOUT", defaultServiceIdleTimeout),
	}
	if err := defaults.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidServiceDefault, err))
	}
	return defaults
}

func newRegistryConfig(registryHost string) registryclient.Config {
	cfg := registryclient.Config{
		URL:      stringEnv("LOCO_REGISTRY_URL", defaultRegistryScheme+registryHost),
		Username: stringEnv("LOCO_REGISTRY_USERNAME", ""),
		Password: stringEnv("LOCO_REGISTRY_PASSWORD", ""),
		Timeout:  positiveDurationEnv("LOCO_REGISTRY_TIMEOUT", defaultRegistryTimeout),
	}
	if (cfg.Username == "") != (cfg.Password == "") {
		panic(errRegistryAuthPartial)
	}
	if cfg.Username == "" {
		return cfg
	}
	if registryHost == "" {
		panic(errRegistryAuthNoHost)
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidRegistry, err))
	}
	return cfg
}

func newImageSweepConfig(registryHost, registryPrefix string) service.ImageSweepConfig {
	return service.ImageSweepConfig{
		RegistryHost:    registryHost,
		RegistryPrefix:  registryPrefix,
		Retention:       nonNegativeInt32Env("LOCO_IMAGE_RETENTION", defaultImageRetention),
		Interval:        positiveDurationEnv("LOCO_IMAGE_SWEEP_INTERVAL", defaultImageSweepInterval),
		BuildBatch:      positiveInt32Env("LOCO_IMAGE_SWEEP_BUILD_BATCH", defaultImageSweepBuildBatch),
		RepositoryBatch: int(positiveInt32Env("LOCO_IMAGE_SWEEP_REPOSITORY_BATCH", defaultImageSweepRepositoryBatch)),
		TagBatch:        int(positiveInt32Env("LOCO_IMAGE_SWEEP_TAG_BATCH", defaultImageSweepTagBatch)),
		TagMinAge:       positiveDurationEnv("LOCO_IMAGE_SWEEP_TAG_MIN_AGE", defaultImageSweepTagMinAge),
	}
}

func newSourceSweepConfig() service.SourceSweepConfig {
	return service.SourceSweepConfig{
		Interval:       positiveDurationEnv("LOCO_SOURCE_SWEEP_INTERVAL", defaultSourceSweepInterval),
		UploadGrace:    positiveDurationEnv("LOCO_SOURCE_SWEEP_UPLOAD_GRACE", defaultSourceUploadGrace),
		OrphanMinAge:   positiveDurationEnv("LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE", defaultSourceOrphanMinAge),
		BuildBatch:     positiveInt32Env("LOCO_SOURCE_SWEEP_BUILD_BATCH", defaultSourceSweepBuildBatch),
		OrphanPageSize: positiveInt32Env("LOCO_SOURCE_SWEEP_ORPHAN_PAGE_SIZE", defaultSourceOrphanPageSize),
		OrphanMaxPages: int(positiveInt32Env("LOCO_SOURCE_SWEEP_ORPHAN_MAX_PAGES", defaultSourceOrphanMaxPages)),
	}
}

func newGithubOAuthConfig() service.GithubOAuthConfig {
	return service.GithubOAuthConfig{
		ClientID:     stringEnv("GH_OAUTH_CLIENT_ID", ""),
		ClientSecret: stringEnv("GH_OAUTH_CLIENT_SECRET", ""),
	}
}

func newSourceBucket(cfg sourcebucket.Config) (service.SourceBucket, error) {
	if cfg.Bucket == "" {
		slog.Warn("LOCO_SOURCE_BUCKET is not set; builds are disabled")
		return nil, nil
	}
	bucket, err := sourcebucket.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("source bucket: %w", err)
	}
	return bucket, nil
}

func newImageRegistry(cfg registryclient.Config, retention int32) (service.ImageRegistry, error) {
	if retention == imageRetentionDisabled {
		slog.Warn("LOCO_IMAGE_RETENTION is 0; image cleanup is disabled by configuration")
		return nil, nil
	}
	if cfg.Username == "" {
		slog.Warn("LOCO_REGISTRY_USERNAME is not set; build images are never deleted from the registry")
		return nil, nil
	}
	client, err := registryclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("registry client: %w", err)
	}
	return client, nil
}

func newCache(cacheType, cacheAddr string, defaultTTL time.Duration) (cache.Cache, error) {
	switch cacheType {
	case cacheTypeValkey:
		return cache.NewValkey(cacheAddr, defaultTTL)
	case cacheTypeMemory:
		return cache.NewMemory(defaultTTL)
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownCacheType, cacheType)
	}
}
