package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/webhooks"
	"github.com/team-loco/loco/internal/buildinfo"
	"golang.org/x/mod/semver"
)

var (
	errCacheAddrMissing   = errors.New("CACHE_ADDR required when CACHE_TYPE=valkey")
	errUnknownCacheType   = errors.New("unknown cache type")
	errInvalidSourceBytes = errors.New("LOCO_SOURCE_MAX_BYTES is not a positive integer")

	errInvalidForcePathStyle   = errors.New("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE is not a boolean")
	errInvalidServiceDefault   = errors.New("invalid service default")
	errRegistryAuthPartial     = errors.New("LOCO_REGISTRY_USERNAME and LOCO_REGISTRY_PASSWORD must be set together")
	errRegistryAuthNoHost      = errors.New("LOCO_REGISTRY_USERNAME is set without LOCO_REGISTRY_HOST")
	errInvalidRegistry         = errors.New("invalid registry configuration")
	errInvalidWebhooksPrivate  = errors.New("WEBHOOK_ALLOW_PRIVATE_NETWORKS is not a boolean")
	errInvalidInstallWebhooks  = errors.New("INSTALL_WEBHOOKS is invalid")
	errInvalidAuthIssuers      = errors.New("AUTH_ISSUERS is invalid")
	errAdminTokenMissing       = errors.New("AUTH_ISSUERS admin tokenEnv names an unset variable")
	errInvalidSignupPolicy     = errors.New("AUTH_SIGNUP_MODE is invalid")
	errInvalidMinCLIVersion    = errors.New("MIN_CLI_VERSION is not a semantic version like v0.0.61")
	errSecretsLocalKeysMissing = errors.New("LOCO_SECRETS_LOCAL_KEYS required when LOCO_SECRETS_KEY_PROVIDER=local")
	errInvalidSecretsLocalKeys = errors.New("LOCO_SECRETS_LOCAL_KEYS is invalid")
	errUnknownSecretsProvider  = errors.New("LOCO_SECRETS_KEY_PROVIDER is invalid")
	errInvalidSecretsTransit   = errors.New("LOCO_SECRETS_TRANSIT_* is invalid")
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

	day                        = 24 * time.Hour
	defaultEventsRetentionDays = 90

	defaultSecretMaxValueBytes     = 64 * 1024
	defaultSecretMaxPerEnvironment = 256
	defaultSecretMaxServiceBytes   = 768 * 1024
	defaultSecretLockTimeout       = 10 * time.Second

	defaultSecretsTransitMount   = "transit"
	defaultSecretsTransitTimeout = 10 * time.Second
	defaultSecretsTransitMargin  = 5 * time.Minute
	defaultSecretsTransitRetry   = 10 * time.Second
	defaultSecretsTransitDEKTTL  = 5 * time.Minute
)

type APIConfig struct {
	Version               string
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
	SourceBucket          sourcebucket.Config
	SourceMaxBytes        int64
	RegistryHost          string
	RegistryPrefix        string
	Registry              registryclient.Config
	ImageSweep            service.ImageSweepConfig
	SourceSweep           service.SourceSweepConfig
	ServiceDefaults       servicedefaults.Defaults
	AuthIssuers           []auth.IssuerConfig
	SignupPolicy          auth.SignupPolicy
	WebURL                string
	EventsRetention       time.Duration
	WebhooksAllowPrivate  bool
	InstallWebhooks       []webhooks.InstallWebhook
	Secrets               secretkeys.Config
	SecretLimits          service.SecretConfig
}

type MigrateConfig struct {
	DatabaseURL string
}

func newMigrateConfig() MigrateConfig {
	return MigrateConfig{DatabaseURL: stringEnv("DATABASE_URL", "")}
}

func newAPIConfig() *APIConfig {
	migrateConfig := newMigrateConfig()
	cacheType, cacheAddr := newCacheConfig()
	registryHost := stringEnv("LOCO_REGISTRY_HOST", "")
	registryPrefix := stringEnv("LOCO_REGISTRY_PREFIX", "")
	sourceMaxBytes := positiveInt64Env("LOCO_SOURCE_MAX_BYTES", defaultSourceMaxBytes, errInvalidSourceBytes)
	sourceBucket := newSourceBucketConfig()
	serviceDefaults := newServiceDefaults()
	registry := newRegistryConfig(registryHost)
	imageSweep := newImageSweepConfig(registryHost, registryPrefix)
	sourceSweep := newSourceSweepConfig()
	logLevel, ok := logLevelEnv("LOG_LEVEL")
	if !ok {
		logLevel = slog.LevelInfo
	}
	corsOrigins := listEnv("CORS_ALLOWED_ORIGINS")
	webhooksAllowPrivate := boolEnv("WEBHOOK_ALLOW_PRIVATE_NETWORKS", false, errInvalidWebhooksPrivate)
	installWebhooks := newInstallWebhooks()
	authIssuers := newAuthIssuers()
	signupPolicy := newSignupPolicy()
	minCLIVersion := newMinCLIVersion()
	eventsRetentionDays := positiveInt32Env("EVENTS_RETENTION_DAYS", defaultEventsRetentionDays)
	eventsRetention := time.Duration(eventsRetentionDays) * day
	secrets := newSecretsConfig()
	secretLimits := service.SecretConfig{
		MaxValueBytes:     positiveInt32Env("LOCO_SECRETS_MAX_VALUE_BYTES", defaultSecretMaxValueBytes),
		MaxPerEnvironment: positiveInt32Env("LOCO_SECRETS_MAX_PER_ENVIRONMENT", defaultSecretMaxPerEnvironment),
		MaxServiceBytes:   positiveInt32Env("LOCO_SECRETS_MAX_SERVICE_BYTES", defaultSecretMaxServiceBytes),
		LockTimeout:       positiveDurationEnv("LOCO_SECRETS_LOCK_TIMEOUT", defaultSecretLockTimeout),
	}

	return &APIConfig{
		Version:               buildinfo.Version(version),
		Env:                   stringEnv("APP_ENV", ""),
		DatabaseURL:           migrateConfig.DatabaseURL,
		Port:                  stringEnv("APP_PORT", ""),
		LogLevel:              logLevel,
		CacheType:             cacheType,
		CacheAddr:             cacheAddr,
		CORSAllowedOrigins:    corsOrigins,
		DefaultPlatformDomain: stringEnv("DEFAULT_PLATFORM_DOMAIN", ""),
		MinCLIVersion:         minCLIVersion,
		PprofAddr:             stringEnv("PPROF_ADDR", ""),
		SourceBucket:          sourceBucket,
		SourceMaxBytes:        sourceMaxBytes,
		RegistryHost:          registryHost,
		RegistryPrefix:        registryPrefix,
		Registry:              registry,
		ImageSweep:            imageSweep,
		SourceSweep:           sourceSweep,
		ServiceDefaults:       serviceDefaults,
		AuthIssuers:           authIssuers,
		SignupPolicy:          signupPolicy,
		WebURL:                stringEnv("WEB_URL", ""),
		EventsRetention:       eventsRetention,
		WebhooksAllowPrivate:  webhooksAllowPrivate,
		InstallWebhooks:       installWebhooks,
		Secrets:               secrets,
		SecretLimits:          secretLimits,
	}
}

func newSecretKeyProvider(ctx context.Context, cfg secretkeys.Config) (secretkeys.Provider, error) {
	if cfg.Provider == "" {
		slog.Warn("LOCO_SECRETS_KEY_PROVIDER is not set; secrets cannot be stored")
		return nil, nil
	}
	provider, err := secretkeys.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("secrets key provider: %w", err)
	}
	return provider, nil
}

func newSecretsConfig() secretkeys.Config {
	provider := stringEnv("LOCO_SECRETS_KEY_PROVIDER", "")
	rawKeys := stringEnv("LOCO_SECRETS_LOCAL_KEYS", "")
	switch provider {
	case "":
		return secretkeys.Config{}
	case secretkeys.ProviderLocal:
		if rawKeys == "" {
			panic(errSecretsLocalKeysMissing)
		}
		keys, err := secretkeys.ParseLocalKeys(rawKeys)
		if err != nil {
			panic(fmt.Errorf("%w: %w", errInvalidSecretsLocalKeys, err))
		}
		return secretkeys.Config{Provider: provider, LocalKeys: keys}
	case secretkeys.ProviderTransit:
		transit := secretkeys.TransitConfig{
			Address: stringEnv("LOCO_SECRETS_TRANSIT_ADDR", ""),
			Mount:   stringEnv("LOCO_SECRETS_TRANSIT_MOUNT", defaultSecretsTransitMount),
			Key:     stringEnv("LOCO_SECRETS_TRANSIT_KEY", ""),
			Token:   stringEnv("LOCO_SECRETS_TRANSIT_TOKEN", ""),
			CAFile:  stringEnv("LOCO_SECRETS_TRANSIT_CA_FILE", ""),
			Timeout: positiveDurationEnv("LOCO_SECRETS_TRANSIT_TIMEOUT", defaultSecretsTransitTimeout),
			RenewMargin: positiveDurationEnv(
				"LOCO_SECRETS_TRANSIT_RENEW_MARGIN",
				defaultSecretsTransitMargin,
			),
			RenewRetry: positiveDurationEnv("LOCO_SECRETS_TRANSIT_RENEW_RETRY", defaultSecretsTransitRetry),
			CacheTTL:   positiveDurationEnv("LOCO_SECRETS_TRANSIT_CACHE_TTL", defaultSecretsTransitDEKTTL),
		}
		if err := transit.Validate(); err != nil {
			panic(fmt.Errorf("%w: %w", errInvalidSecretsTransit, err))
		}
		return secretkeys.Config{Provider: provider, Transit: transit}
	default:
		panic(fmt.Errorf("%w: %q", errUnknownSecretsProvider, provider))
	}
}

func newMinCLIVersion() string {
	minCLIVersion := stringEnv("MIN_CLI_VERSION", "")
	if minCLIVersion != "" && !semver.IsValid(minCLIVersion) {
		panic(fmt.Errorf("%w: %q", errInvalidMinCLIVersion, minCLIVersion))
	}
	return minCLIVersion
}

func newInstallWebhooks() []webhooks.InstallWebhook {
	raw := stringEnv("INSTALL_WEBHOOKS", "")
	hooks, err := webhooks.ParseInstallWebhooks(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidInstallWebhooks, err))
	}
	return hooks
}

func newAuthIssuers() []auth.IssuerConfig {
	raw := stringEnv("AUTH_ISSUERS", "")
	issuers, err := auth.ParseIssuers(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidAuthIssuers, err))
	}
	for _, ic := range issuers {
		if ic.Admin == nil {
			continue
		}
		ic.Admin.Token = stringEnv(ic.Admin.TokenEnv, "")
		if ic.Admin.Token == "" {
			panic(fmt.Errorf("%w: issuer %s: %q", errAdminTokenMissing, ic.Issuer, ic.Admin.TokenEnv))
		}
	}
	return issuers
}

func newSignupPolicy() auth.SignupPolicy {
	mode := stringEnv("AUTH_SIGNUP_MODE", "")
	domains := stringEnv("AUTH_SIGNUP_DOMAINS", "")
	policy, err := auth.ParseSignupPolicy(mode, domains)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidSignupPolicy, err))
	}
	return policy
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
