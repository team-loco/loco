package main

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/guardrails"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/sourcebucket"
	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/sweepers"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/webhooks"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/internal/buildinfo"
	"github.com/team-loco/loco/internal/clickhouse/migrations"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/workloadplatform"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"golang.org/x/mod/semver"
)

var (
	errAPITokenLifetime        = errors.New("LOCO_API_TOKEN_MAX_LIFETIME must be at least one second")
	errStreamAuthTimeout       = errors.New("LOCO_STREAM_AUTH_TIMEOUT must not exceed LOCO_STREAM_AUTH_INTERVAL")
	errDatabaseURLMissing      = errors.New("DATABASE_URL is required")
	errDatabasePoolSize        = errors.New("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	errInvalidPublicImageBytes = errors.New("LOCO_PUBLIC_IMAGE_MAX_BYTES is not a positive integer")
	errInvalidSourceBytes      = errors.New("LOCO_SOURCE_MAX_BYTES is not a positive integer")
	errClickHouseDatabase      = errors.New("CLICKHOUSE_DB must be a ClickHouse identifier when CLICKHOUSE_URL is set")
	errClickHouseTimeout       = errors.New("CLICKHOUSE_QUERY_TIMEOUT must be a whole number of seconds")

	errInvalidServiceDefault  = errors.New("invalid service default")
	errRegistryAuthPartial    = errors.New("LOCO_REGISTRY_USERNAME and LOCO_REGISTRY_PASSWORD must be set together")
	errRegistryAuthNoHost     = errors.New("LOCO_REGISTRY_USERNAME is set without LOCO_REGISTRY_HOST")
	errInvalidRegistry        = errors.New("invalid registry configuration")
	errInvalidInstallWebhooks = errors.New("INSTALL_WEBHOOKS is invalid")
	errInvalidOrgLinks        = errors.New("ORG_LINKS is invalid")
	errInvalidAuthIssuers     = errors.New("AUTH_ISSUERS is invalid")
	errInvalidSignupPolicy    = errors.New("AUTH_SIGNUP_MODE is invalid")
	errInvalidDeploymentMode  = errors.New("LOCO_DEPLOYMENT_MODE must be self-hosted, dedicated or multi-tenant")
	errInvalidMinCLIVersion   = errors.New("MIN_CLI_VERSION is not a semantic version like v0.0.61")
	errInvalidSecretsTransit  = errors.New("LOCO_SECRETS_TRANSIT_* is invalid")
	errInvalidSystemAdmins    = errors.New(systemAdminsEnv + " is invalid")
	errInvalidIngestKey       = errors.New("LOCO_INGEST_TRANSIT_KEY is invalid")
	errInvalidRegistryAuth    = errors.New("LOCO_REGISTRY_AUTH must be static or token")
	errInvalidRegistryKey     = errors.New("LOCO_REGISTRY_TRANSIT_KEY is invalid")
	errInvalidRegistryVersion = errors.New("LOCO_REGISTRY_TRANSIT_KEY_VERSION must be a positive integer")
	errMissingRegistryService = errors.New("LOCO_REGISTRY_TOKEN_SERVICE is required for LOCO_REGISTRY_AUTH=token")
	errRegistryTokenNoHost    = errors.New("LOCO_REGISTRY_HOST is required for LOCO_REGISTRY_AUTH=token")
	errRegistryKeyShared      = errors.New(
		"LOCO_REGISTRY_TRANSIT_KEY must differ from LOCO_SECRETS_TRANSIT_KEY and LOCO_INGEST_TRANSIT_KEY",
	)
	errRegistryStaticOnly = errors.New(
		"LOCO_REGISTRY_USERNAME and LOCO_REGISTRY_PASSWORD are only for LOCO_REGISTRY_AUTH=static",
	)
)

const (
	defaultSourceMaxBytes = 200 * 1024 * 1024

	defaultServiceCPU         = "100m"
	defaultServiceMemory      = "256Mi"
	defaultServiceMinReplicas = 1
	defaultServiceMaxReplicas = 1
	defaultServiceCPUTarget   = 70
	defaultServicePathPrefix  = "/"
	defaultServiceIdleTimeout = 60
	defaultHealthPath         = "/health"
	defaultHealthInterval     = 30
	defaultHealthTimeout      = 5
	defaultHealthFailThresh   = 3
	defaultHealthGracePeriod  = 0
	defaultApplyAttempts      = 2
	maxApplyAttempts          = 10
	defaultApplyRetryDelay    = int32(5 * time.Minute / time.Second)
	defaultMaxRestarts        = 2
	defaultDeployTimeout      = int32(10 * time.Minute / time.Second)
	defaultSchemaURL          = "https://loco.build/schemas/loco.v1.json"

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

	defaultStreamAuthInterval = 5 * time.Second
	defaultStreamAuthTimeout  = 2 * time.Second
	defaultMaxStreams         = 500
	defaultMaxCallerStreams   = 50

	defaultEventsRetention = 90 * 24 * time.Hour

	defaultDatabaseMaxConns         = 25
	defaultDatabaseMinConns         = 20
	defaultDatabaseStatementTimeout = 15 * time.Second

	defaultClickHouseMaxConns        = 16
	defaultClickHouseQueryTimeout    = 10 * time.Second
	defaultObservabilityLimit        = 1000
	defaultObservabilityMaxLimit     = 10000
	defaultObservabilityMaxTimeRange = 24 * time.Hour
	defaultObservabilityMaxBuckets   = 1440
	defaultObservabilityMaxTail      = 30 * time.Minute

	defaultAPITokenMaxLifetime        = 365 * 24 * time.Hour
	defaultSessionAccessTokenLifetime = time.Hour
	defaultSessionRefreshLifetime     = 7 * 24 * time.Hour
	defaultSessionRefreshReuseGrace   = 10 * time.Second
	defaultTokenLastUsedInterval      = 5 * time.Minute
	defaultTokenCleanupInterval       = time.Minute

	defaultPublicImageMaxBytes   = 1 << 30
	defaultImageResolveTimeout   = 15 * time.Second
	defaultRPCTimeout            = 30 * time.Second
	defaultShutdownDrainTimeout  = 30 * time.Second
	defaultPlacementPollInterval = 5 * time.Second
	defaultActivityPollInterval  = 30 * time.Second

	defaultWebhookTimeout      = 10 * time.Second
	defaultWebhookBatch        = 50
	defaultWebhookPollInterval = 2 * time.Second

	defaultSecretMaxValueBytes     = 64 * 1024
	defaultSecretMaxPlacementBytes = 768 * 1024
	defaultSecretLockTimeout       = 10 * time.Second

	defaultSecretsTransitMount   = "transit"
	defaultSecretsTransitTimeout = 10 * time.Second
	defaultSecretsTransitStartup = 30 * time.Second
	defaultSecretsTransitMargin  = 5 * time.Minute
	defaultSecretsTransitRetry   = 10 * time.Second
	defaultSecretsTransitDEKTTL  = 5 * time.Minute

	defaultHeartbeatInterval         = 30 * time.Second
	defaultClusterJoinTokenTTL       = time.Hour
	defaultClusterJoinTokenMaxTTL    = 7 * 24 * time.Hour
	defaultWorkspaceNamespaceGrace   = 24 * time.Hour
	defaultAgentAssertionMaxLifetime = 5 * time.Minute
	defaultInvitationTTL             = 168 * time.Hour

	systemAdminsEnv            = "LOCO_SYSTEM_ADMINS"
	defaultIngestTokenLifetime = time.Hour

	defaultRegistryAccessTokenLifetime    = 5 * time.Minute
	defaultRegistryPullCredentialLifetime = 24 * time.Hour
	defaultRegistryCredentialSweep        = 10 * time.Minute
)

var defaultWebhookRetryDelays = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// ClickHouseConfig is the read-only connection the observability RPCs query. An empty URL turns
// them off.
type ClickHouseConfig struct {
	URL          string
	Database     string
	MaxConns     int
	QueryTimeout time.Duration
}

type IngestConfig struct {
	TransitKey    string
	TokenLifetime time.Duration
}

type RegistryAuthConfig struct {
	Mode                   string
	TransitKey             transit.PinnedKey
	Service                string
	AccessTokenLifetime    time.Duration
	PullCredentialLifetime time.Duration
	CredentialSweep        time.Duration
}

func (c RegistryAuthConfig) TokenMode() bool {
	return c.Mode == locoControllerV1.RegistryAuthToken
}

type APIConfig struct {
	TargetPlatform        workloadplatform.Platform
	ClickHouse            ClickHouseConfig
	Observability         service.ObservabilityConfig
	Agent                 service.AgentConfig
	Ingest                IngestConfig
	Version               string
	Env                   string // Environment (e.g., dev, prod)
	Database              db.Config
	DatabaseListenConfig  *pgx.ConnConfig
	PlacementPollInterval time.Duration
	ActivityPollInterval  time.Duration
	Tokens                tvm.Config
	TokenCleanupInterval  time.Duration
	ImageResolveTimeout   time.Duration
	PublicImageMaxBytes   int64
	RPCTimeout            time.Duration
	ShutdownDrainTimeout  time.Duration
	LogLevel              slog.Level
	Port                  string
	CORSAllowedOrigins    []string // CORS allowed origins (e.g., http://localhost:5173)
	MinCLIVersion         string
	DefaultRegion         string
	DeploymentMode        configv1.DeploymentMode
	PprofAddr             string
	SourceBucket          sourcebucket.Config
	SourceMaxBytes        int64
	RegistryHost          string
	RegistryPrefix        string
	Registry              registryclient.Config
	RegistryAuth          RegistryAuthConfig
	ImageSweep            sweepers.ImageSweepConfig
	SourceSweep           sweepers.SourceSweepConfig
	ServiceDefaults       servicedefaults.Defaults
	AuthIssuers           []auth.IssuerConfig
	StreamAuth            auth.StreamConfig
	StreamLimits          interceptor.StreamLimits
	SignupPolicy          auth.SignupPolicy
	WebURL                string
	TrustedProxyHops      int
	Invitations           service.InvitationConfig
	SchemaURL             string
	EventsRetention       time.Duration
	WebhooksAllowPrivate  bool
	Webhooks              webhooks.Config
	InstallWebhooks       []webhooks.InstallWebhook
	OrgLinks              []service.OrgLink
	Transit               transit.Config
	Secrets               secretkeys.Config
	SecretLimits          service.SecretConfig
	CountLimits           *configv1.CountLimits
	HeartbeatInterval     time.Duration
	SystemAdmins          auth.SystemAdmins
	TracingEnabled        bool
	MetricsEnabled        bool
}

func newAPIConfig() *APIConfig {
	targetPlatform, err := workloadplatform.Parse(
		envconfig.String("LOCO_TARGET_PLATFORM", string(workloadplatform.AMD64)),
	)
	if err != nil {
		panic(err)
	}
	database := newDatabaseConfig()
	ingestConfig := newIngestConfig()
	databaseListenConfig := newDatabaseListenConfig(database.URL)
	registryHost := envconfig.String("LOCO_REGISTRY_HOST", "")
	registryPrefix := envconfig.String("LOCO_REGISTRY_PREFIX", "")
	sourceMaxBytes := positiveInt64Env("LOCO_SOURCE_MAX_BYTES", defaultSourceMaxBytes, errInvalidSourceBytes)
	publicImageMaxBytes := positiveInt64Env(
		"LOCO_PUBLIC_IMAGE_MAX_BYTES", defaultPublicImageMaxBytes, errInvalidPublicImageBytes,
	)
	sourceBucket := newSourceBucketConfig()
	serviceDefaults := newServiceDefaults()
	serviceDefaults.TargetPlatform = targetPlatform
	agentConfig := newAgentConfig(serviceDefaults)
	registry := newRegistryConfig(registryHost)
	imageSweep := newImageSweepConfig(registryHost, registryPrefix)
	sourceSweep := newSourceSweepConfig()
	logLevel, ok := logLevelEnv("LOG_LEVEL")
	if !ok {
		logLevel = slog.LevelInfo
	}
	corsOrigins := listEnv("CORS_ALLOWED_ORIGINS")
	webhooksAllowPrivate := envconfig.Bool("WEBHOOK_ALLOW_PRIVATE_NETWORKS", false)
	installWebhooks := newInstallWebhooks()
	orgLinks := newOrgLinks()
	authIssuers := newAuthIssuers()
	streamAuth := auth.StreamConfig{
		Interval: envconfig.PositiveDuration("LOCO_STREAM_AUTH_INTERVAL", defaultStreamAuthInterval),
		Timeout:  envconfig.PositiveDuration("LOCO_STREAM_AUTH_TIMEOUT", defaultStreamAuthTimeout),
	}
	// A stale recheck in flight is followed by one more, so stale access lasts at most interval plus timeout.
	if streamAuth.Timeout > streamAuth.Interval {
		panic(fmt.Errorf("%w: timeout %s, interval %s", errStreamAuthTimeout, streamAuth.Timeout, streamAuth.Interval))
	}
	streamLimits := interceptor.StreamLimits{
		PerReplica:   positiveInt32Env("LOCO_MAX_STREAMS", defaultMaxStreams),
		PerPrincipal: positiveInt32Env("LOCO_MAX_CALLER_STREAMS", defaultMaxCallerStreams),
	}
	signupPolicy := newSignupPolicy()
	minCLIVersion := newMinCLIVersion()
	eventsRetention := envconfig.PositiveDuration("EVENTS_RETENTION", defaultEventsRetention)
	transitConfig := newTransitConfig()
	secrets := newSecretsConfig()
	registryAuth := newRegistryAuthConfig(registry, registryHost, secrets.Key, ingestConfig.TransitKey)
	agentConfig.RegistryTokenMode = registryAuth.TokenMode()
	heartbeatInterval := envconfig.PositiveDuration("LOCO_AGENT_HEARTBEAT_INTERVAL", defaultHeartbeatInterval)
	systemAdmins := newSystemAdmins()
	tracingEnabled := newTracingEnabled()
	webURL := envconfig.String("WEB_URL", "")
	if _, configured := auth.WebIssuer(authIssuers); configured {
		if err := auth.ValidateWebURL(webURL); err != nil {
			panic(err)
		}
	}
	invitations := newInvitationConfig(webURL)
	secretLimits := service.SecretConfig{
		MaxValueBytes:     positiveInt32Env("LOCO_SECRETS_MAX_VALUE_BYTES", defaultSecretMaxValueBytes),
		MaxPlacementBytes: positiveInt32Env("LOCO_SECRETS_MAX_PLACEMENT_BYTES", defaultSecretMaxPlacementBytes),
		LockTimeout:       envconfig.PositiveDuration("LOCO_SECRETS_LOCK_TIMEOUT", defaultSecretLockTimeout),
	}

	return &APIConfig{
		TargetPlatform:        targetPlatform,
		Agent:                 agentConfig,
		Ingest:                ingestConfig,
		Version:               buildinfo.Version(version),
		Env:                   envconfig.String("APP_ENV", ""),
		TrustedProxyHops:      int(nonNegativeInt32Env("LOCO_TRUSTED_PROXY_HOPS", 0)),
		Database:              database,
		DatabaseListenConfig:  databaseListenConfig,
		PlacementPollInterval: envconfig.PositiveDuration("LOCO_PLACEMENT_POLL_INTERVAL", defaultPlacementPollInterval),
		ActivityPollInterval:  envconfig.PositiveDuration("LOCO_ACTIVITY_POLL_INTERVAL", defaultActivityPollInterval),
		Tokens:                newTokenConfig(),
		TokenCleanupInterval:  envconfig.PositiveDuration("LOCO_TOKEN_CLEANUP_INTERVAL", defaultTokenCleanupInterval),
		PublicImageMaxBytes:   publicImageMaxBytes,
		ImageResolveTimeout:   envconfig.PositiveDuration("LOCO_IMAGE_RESOLVE_TIMEOUT", defaultImageResolveTimeout),
		RPCTimeout:            envconfig.PositiveDuration("LOCO_RPC_TIMEOUT", defaultRPCTimeout),
		ShutdownDrainTimeout:  envconfig.PositiveDuration("LOCO_SHUTDOWN_DRAIN_TIMEOUT", defaultShutdownDrainTimeout),
		Port:                  envconfig.String("APP_PORT", ""),
		ClickHouse:            newClickHouseConfig(),
		Observability:         newObservabilityConfig(),
		LogLevel:              logLevel,
		CORSAllowedOrigins:    corsOrigins,
		MinCLIVersion:         minCLIVersion,
		DefaultRegion:         envconfig.String("LOCO_DEFAULT_REGION", ""),
		DeploymentMode:        newDeploymentMode(),
		PprofAddr:             envconfig.String("PPROF_ADDR", ""),
		SourceBucket:          sourceBucket,
		SourceMaxBytes:        sourceMaxBytes,
		RegistryHost:          registryHost,
		RegistryPrefix:        registryPrefix,
		Registry:              registry,
		RegistryAuth:          registryAuth,
		ImageSweep:            imageSweep,
		SourceSweep:           sourceSweep,
		ServiceDefaults:       serviceDefaults,
		AuthIssuers:           authIssuers,
		StreamAuth:            streamAuth,
		StreamLimits:          streamLimits,
		SignupPolicy:          signupPolicy,
		WebURL:                webURL,
		Invitations:           invitations,
		SchemaURL:             envconfig.String("LOCO_SCHEMA_URL", defaultSchemaURL),
		EventsRetention:       eventsRetention,
		WebhooksAllowPrivate:  webhooksAllowPrivate,
		Webhooks:              newWebhookConfig(),
		InstallWebhooks:       installWebhooks,
		OrgLinks:              orgLinks,
		Transit:               transitConfig,
		Secrets:               secrets,
		SecretLimits:          secretLimits,
		CountLimits:           newCountLimits(),
		HeartbeatInterval:     heartbeatInterval,
		SystemAdmins:          systemAdmins,
		TracingEnabled:        tracingEnabled,
		MetricsEnabled:        newMetricsEnabled(),
	}
}

// newCountLimits reads each count limit's environment variable over its built-in default.
func newCountLimits() *configv1.CountLimits {
	counts := limits.Defaults()
	for _, limit := range limits.All {
		limit.Set(counts, positiveInt32Env(limit.Env, limit.Value(counts)))
	}
	return counts
}

func newClickHouseConfig() ClickHouseConfig {
	cfg := ClickHouseConfig{
		URL:          envconfig.String("CLICKHOUSE_URL", ""),
		Database:     envconfig.String("CLICKHOUSE_DB", ""),
		MaxConns:     int(positiveInt32Env("CLICKHOUSE_MAX_CONNS", defaultClickHouseMaxConns)),
		QueryTimeout: envconfig.PositiveDuration("CLICKHOUSE_QUERY_TIMEOUT", defaultClickHouseQueryTimeout),
	}
	if cfg.URL != "" && !migrations.IsIdentifier(cfg.Database) {
		panic(fmt.Errorf("%w: %q", errClickHouseDatabase, cfg.Database))
	}
	// ClickHouse takes the timeout as max_execution_time in whole seconds.
	if cfg.QueryTimeout%time.Second != 0 {
		panic(fmt.Errorf("%w: %s", errClickHouseTimeout, cfg.QueryTimeout))
	}
	return cfg
}

func newObservabilityConfig() service.ObservabilityConfig {
	return service.ObservabilityConfig{
		Limits: guardrails.Limits{
			DefaultLimit: positiveInt32Env("LOCO_OBSERVABILITY_DEFAULT_LIMIT", defaultObservabilityLimit),
			MaxLimit:     positiveInt32Env("LOCO_OBSERVABILITY_MAX_LIMIT", defaultObservabilityMaxLimit),
			MaxTimeRange: envconfig.PositiveDuration(
				"LOCO_OBSERVABILITY_MAX_TIME_RANGE",
				defaultObservabilityMaxTimeRange,
			),
			MaxBuckets: positiveInt32Env("LOCO_OBSERVABILITY_MAX_BUCKETS", defaultObservabilityMaxBuckets),
		},
		MaxTailDuration: envconfig.PositiveDuration(
			"LOCO_OBSERVABILITY_MAX_TAIL_DURATION",
			defaultObservabilityMaxTail,
		),
	}
}

func newDatabaseConfig() db.Config {
	maxConns := positiveInt32Env("DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	cfg := db.Config{
		URL:              envconfig.String("DATABASE_URL", ""),
		MaxConns:         maxConns,
		MinConns:         nonNegativeInt32Env("DATABASE_MIN_CONNS", min(defaultDatabaseMinConns, maxConns)),
		StatementTimeout: envconfig.PositiveDuration("DATABASE_STATEMENT_TIMEOUT", defaultDatabaseStatementTimeout),
	}
	if cfg.URL == "" {
		panic(errDatabaseURLMissing)
	}
	if cfg.MinConns > cfg.MaxConns {
		panic(fmt.Errorf("%w: %d > %d", errDatabasePoolSize, cfg.MinConns, cfg.MaxConns))
	}
	return cfg
}

func newTokenConfig() tvm.Config {
	maximum := envconfig.PositiveDuration("LOCO_API_TOKEN_MAX_LIFETIME", defaultAPITokenMaxLifetime)
	if maximum < time.Second {
		panic(errAPITokenLifetime)
	}
	return tvm.Config{
		MaxAPITokenDuration: maximum,
		SessionAccessTokenDuration: envconfig.PositiveDuration(
			"LOCO_SESSION_ACCESS_TOKEN_LIFETIME",
			defaultSessionAccessTokenLifetime,
		),
		SessionRefreshTokenDuration: envconfig.PositiveDuration(
			"LOCO_SESSION_REFRESH_TOKEN_LIFETIME",
			defaultSessionRefreshLifetime,
		),
		RefreshReuseGrace: envconfig.PositiveDuration(
			"LOCO_SESSION_REFRESH_REUSE_GRACE",
			defaultSessionRefreshReuseGrace,
		),
		LastUsedUpdateInterval: envconfig.PositiveDuration(
			"LOCO_TOKEN_LAST_USED_INTERVAL",
			defaultTokenLastUsedInterval,
		),
	}
}

func newWebhookConfig() webhooks.Config {
	return webhooks.Config{
		RequestTimeout: envconfig.PositiveDuration("WEBHOOK_TIMEOUT", defaultWebhookTimeout),
		ClaimBatch:     positiveInt32Env("WEBHOOK_BATCH", defaultWebhookBatch),
		RetryDelays:    positiveDurationListEnv("WEBHOOK_RETRY_DELAYS", defaultWebhookRetryDelays),
		PollInterval:   envconfig.PositiveDuration("WEBHOOK_POLL_INTERVAL", defaultWebhookPollInterval),
	}
}

func newInvitationConfig(webURL string) service.InvitationConfig {
	return service.InvitationConfig{
		TTL:    envconfig.PositiveDuration("INVITATION_TTL", defaultInvitationTTL),
		WebURL: webURL,
	}
}

func newSystemAdmins() auth.SystemAdmins {
	admins, err := auth.ParseSystemAdmins(listEnv(systemAdminsEnv))
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidSystemAdmins, err))
	}
	return admins
}

func newTransitConfig() transit.Config {
	cfg := transit.Config{
		Address: envconfig.String("LOCO_SECRETS_TRANSIT_ADDR", ""),
		Mount:   envconfig.String("LOCO_SECRETS_TRANSIT_MOUNT", defaultSecretsTransitMount),
		Auth:    envconfig.String("LOCO_SECRETS_TRANSIT_AUTH", ""),
		Token:   envconfig.String("LOCO_SECRETS_TRANSIT_TOKEN", ""),
		Kubernetes: transit.KubernetesConfig{
			Mount:     envconfig.String("LOCO_SECRETS_TRANSIT_KUBERNETES_MOUNT", ""),
			Role:      envconfig.String("LOCO_SECRETS_TRANSIT_KUBERNETES_ROLE", ""),
			TokenFile: envconfig.String("LOCO_SECRETS_TRANSIT_KUBERNETES_TOKEN_FILE", ""),
		},
		CAFile:  envconfig.String("LOCO_SECRETS_TRANSIT_CA_FILE", ""),
		Timeout: envconfig.PositiveDuration("LOCO_SECRETS_TRANSIT_TIMEOUT", defaultSecretsTransitTimeout),
		StartupTimeout: envconfig.PositiveDuration(
			"LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT",
			defaultSecretsTransitStartup,
		),
		RenewMargin: envconfig.PositiveDuration(
			"LOCO_SECRETS_TRANSIT_RENEW_MARGIN",
			defaultSecretsTransitMargin,
		),
		RenewRetry: envconfig.PositiveDuration("LOCO_SECRETS_TRANSIT_RENEW_RETRY", defaultSecretsTransitRetry),
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidSecretsTransit, err))
	}
	return cfg
}

func newSecretsConfig() secretkeys.Config {
	cfg := secretkeys.Config{
		Key:      envconfig.String("LOCO_SECRETS_TRANSIT_KEY", ""),
		CacheTTL: envconfig.PositiveDuration("LOCO_SECRETS_TRANSIT_CACHE_TTL", defaultSecretsTransitDEKTTL),
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidSecretsTransit, err))
	}
	return cfg
}

func newMinCLIVersion() string {
	minCLIVersion := envconfig.String("MIN_CLI_VERSION", "")
	if minCLIVersion != "" && !semver.IsValid(minCLIVersion) {
		panic(fmt.Errorf("%w: %q", errInvalidMinCLIVersion, minCLIVersion))
	}
	return minCLIVersion
}

func newInstallWebhooks() []webhooks.InstallWebhook {
	raw := envconfig.String("INSTALL_WEBHOOKS", "")
	hooks, err := webhooks.ParseInstallWebhooks(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidInstallWebhooks, err))
	}
	return hooks
}

func newOrgLinks() []service.OrgLink {
	raw := envconfig.String("ORG_LINKS", "")
	links, err := service.ParseOrgLinks(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidOrgLinks, err))
	}
	return links
}

func newAuthIssuers() []auth.IssuerConfig {
	raw := envconfig.String("AUTH_ISSUERS", "")
	issuers, err := auth.ParseIssuers(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidAuthIssuers, err))
	}
	return issuers
}

func newSignupPolicy() auth.SignupPolicy {
	mode := envconfig.String("AUTH_SIGNUP_MODE", "")
	policy, err := auth.ParseSignupPolicy(mode)
	if err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidSignupPolicy, err))
	}
	return policy
}

var deploymentModes = map[string]configv1.DeploymentMode{
	"self-hosted":  configv1.DeploymentMode_DEPLOYMENT_MODE_SELF_HOSTED,
	"dedicated":    configv1.DeploymentMode_DEPLOYMENT_MODE_DEDICATED,
	"multi-tenant": configv1.DeploymentMode_DEPLOYMENT_MODE_MULTI_TENANT,
}

func newDeploymentMode() configv1.DeploymentMode {
	raw := envconfig.String("LOCO_DEPLOYMENT_MODE", "self-hosted")
	mode, ok := deploymentModes[raw]
	if !ok {
		panic(fmt.Errorf("%w: %q", errInvalidDeploymentMode, raw))
	}
	return mode
}

func newSourceBucketConfig() sourcebucket.Config {
	forcePathStyle := envconfig.Bool("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE", false)
	cfg := sourcebucket.Config{
		Endpoint:        envconfig.String("LOCO_SOURCE_BUCKET_ENDPOINT", ""),
		Bucket:          envconfig.String("LOCO_SOURCE_BUCKET", ""),
		Region:          envconfig.String("LOCO_SOURCE_BUCKET_REGION", ""),
		AccessKeyID:     envconfig.String("LOCO_SOURCE_BUCKET_ACCESS_KEY_ID", ""),
		SecretAccessKey: envconfig.String("LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY", ""),
		ForcePathStyle:  forcePathStyle,
	}
	if cfg.Bucket != "" {
		if err := cfg.Validate(); err != nil {
			panic(err)
		}
	}
	return cfg
}

func applyAttemptsEnv() int32 {
	attempts := positiveInt32Env("LOCO_APPLY_ATTEMPTS", defaultApplyAttempts)
	if attempts > maxApplyAttempts {
		panic(fmt.Errorf("LOCO_APPLY_ATTEMPTS %d exceeds %d", attempts, maxApplyAttempts))
	}
	return attempts
}

func newServiceDefaults() servicedefaults.Defaults {
	defaults := servicedefaults.Defaults{
		CPU:                      envconfig.String("LOCO_DEFAULT_CPU", defaultServiceCPU),
		Memory:                   envconfig.String("LOCO_DEFAULT_MEMORY", defaultServiceMemory),
		MinReplicas:              envconfig.Int32("LOCO_DEFAULT_MIN_REPLICAS", defaultServiceMinReplicas),
		MaxReplicas:              envconfig.Int32("LOCO_DEFAULT_MAX_REPLICAS", defaultServiceMaxReplicas),
		CPUTarget:                envconfig.Int32("LOCO_DEFAULT_CPU_TARGET", defaultServiceCPUTarget),
		PathPrefix:               envconfig.String("LOCO_DEFAULT_PATH_PREFIX", defaultServicePathPrefix),
		IdleTimeout:              envconfig.Int32("LOCO_DEFAULT_IDLE_TIMEOUT", defaultServiceIdleTimeout),
		HealthPath:               envconfig.String("LOCO_DEFAULT_HEALTH_PATH", defaultHealthPath),
		HealthInterval:           envconfig.Int32("LOCO_DEFAULT_HEALTH_INTERVAL", defaultHealthInterval),
		HealthTimeout:            envconfig.Int32("LOCO_DEFAULT_HEALTH_TIMEOUT", defaultHealthTimeout),
		HealthFailThreshold:      envconfig.Int32("LOCO_DEFAULT_HEALTH_FAIL_THRESHOLD", defaultHealthFailThresh),
		HealthStartupGracePeriod: envconfig.Int32("LOCO_DEFAULT_HEALTH_STARTUP_GRACE_PERIOD", defaultHealthGracePeriod),
		MaxRestarts:              positiveInt32Env("LOCO_DEFAULT_MAX_RESTARTS", defaultMaxRestarts),
		DeployTimeout:            positiveInt32Env("LOCO_DEFAULT_DEPLOY_TIMEOUT", defaultDeployTimeout),
	}
	if err := defaults.Validate(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidServiceDefault, err))
	}
	return defaults
}

func newRegistryConfig(registryHost string) registryclient.Config {
	cfg := registryclient.Config{
		URL:      envconfig.String("LOCO_REGISTRY_URL", defaultRegistryScheme+registryHost),
		Username: envconfig.String("LOCO_REGISTRY_USERNAME", ""),
		Password: envconfig.String("LOCO_REGISTRY_PASSWORD", ""),
		Timeout:  envconfig.PositiveDuration("LOCO_REGISTRY_TIMEOUT", defaultRegistryTimeout),
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

func newRegistryAuthConfig(
	registry registryclient.Config,
	registryHost, secretsKey, ingestKey string,
) RegistryAuthConfig {
	mode := envconfig.String("LOCO_REGISTRY_AUTH", locoControllerV1.RegistryAuthStatic)
	switch mode {
	case locoControllerV1.RegistryAuthStatic:
		return RegistryAuthConfig{Mode: mode}
	case locoControllerV1.RegistryAuthToken:
		return newRegistryTokenAuthConfig(registry, registryHost, secretsKey, ingestKey)
	default:
		panic(fmt.Errorf("%w: %q", errInvalidRegistryAuth, mode))
	}
}

func newRegistryTokenAuthConfig(
	registry registryclient.Config,
	registryHost, secretsKey, ingestKey string,
) RegistryAuthConfig {
	if registry.Username != "" || registry.Password != "" {
		panic(errRegistryStaticOnly)
	}
	if registryHost == "" {
		panic(errRegistryTokenNoHost)
	}
	if err := registry.ValidateEndpoint(); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidRegistry, err))
	}
	key := envconfig.String("LOCO_REGISTRY_TRANSIT_KEY", "")
	if err := transit.ValidateKeyName(key); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidRegistryKey, err))
	}
	if key == secretsKey || key == ingestKey {
		panic(fmt.Errorf("%w: %q", errRegistryKeyShared, key))
	}
	version := envconfig.Int32("LOCO_REGISTRY_TRANSIT_KEY_VERSION", 0)
	if version <= 0 {
		panic(fmt.Errorf("%w: %d", errInvalidRegistryVersion, version))
	}
	tokenService := envconfig.String("LOCO_REGISTRY_TOKEN_SERVICE", "")
	if tokenService == "" {
		panic(errMissingRegistryService)
	}
	accessLifetime := envconfig.PositiveDuration(
		"LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME",
		defaultRegistryAccessTokenLifetime,
	)
	pullLifetime := envconfig.PositiveDuration(
		"LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME",
		defaultRegistryPullCredentialLifetime,
	)
	sweepInterval := envconfig.PositiveDuration(
		"LOCO_REGISTRY_CREDENTIAL_SWEEP_INTERVAL",
		defaultRegistryCredentialSweep,
	)
	return RegistryAuthConfig{
		Mode:                   locoControllerV1.RegistryAuthToken,
		TransitKey:             transit.PinnedKey{Name: key, Version: int(version)},
		Service:                tokenService,
		AccessTokenLifetime:    accessLifetime,
		PullCredentialLifetime: pullLifetime,
		CredentialSweep:        sweepInterval,
	}
}

func newImageSweepConfig(registryHost, registryPrefix string) sweepers.ImageSweepConfig {
	return sweepers.ImageSweepConfig{
		RegistryHost:    registryHost,
		RegistryPrefix:  registryPrefix,
		Retention:       nonNegativeInt32Env("LOCO_IMAGE_RETENTION", defaultImageRetention),
		Interval:        envconfig.PositiveDuration("LOCO_IMAGE_SWEEP_INTERVAL", defaultImageSweepInterval),
		BuildBatch:      positiveInt32Env("LOCO_IMAGE_SWEEP_BUILD_BATCH", defaultImageSweepBuildBatch),
		RepositoryBatch: int(positiveInt32Env("LOCO_IMAGE_SWEEP_REPOSITORY_BATCH", defaultImageSweepRepositoryBatch)),
		TagBatch:        int(positiveInt32Env("LOCO_IMAGE_SWEEP_TAG_BATCH", defaultImageSweepTagBatch)),
		TagMinAge:       envconfig.PositiveDuration("LOCO_IMAGE_SWEEP_TAG_MIN_AGE", defaultImageSweepTagMinAge),
	}
}

func newSourceSweepConfig() sweepers.SourceSweepConfig {
	return sweepers.SourceSweepConfig{
		Interval:       envconfig.PositiveDuration("LOCO_SOURCE_SWEEP_INTERVAL", defaultSourceSweepInterval),
		UploadGrace:    envconfig.PositiveDuration("LOCO_SOURCE_SWEEP_UPLOAD_GRACE", defaultSourceUploadGrace),
		OrphanMinAge:   envconfig.PositiveDuration("LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE", defaultSourceOrphanMinAge),
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

func newImageRegistry(cfg registryclient.Config, retention int32) (sweepers.ImageRegistry, error) {
	if retention == imageRetentionDisabled {
		slog.Warn("LOCO_IMAGE_RETENTION is 0; image cleanup is disabled by configuration")
		return nil, nil
	}
	if cfg.Username == "" && cfg.TokenSource == nil {
		slog.Warn("LOCO_REGISTRY_USERNAME is not set; build images are never deleted from the registry")
		return nil, nil
	}
	client, err := registryclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("registry client: %w", err)
	}
	return client, nil
}

func newDatabaseListenConfig(databaseURL string) *pgx.ConnConfig {
	listenURL := envconfig.String("DATABASE_LISTEN_URL", databaseURL)
	config, err := pgx.ParseConfig(listenURL)
	if err != nil {
		panic(fmt.Errorf("invalid database listener URL: %w", err))
	}
	return config
}

func newIngestConfig() IngestConfig {
	lifetime := envconfig.PositiveDuration("LOCO_INGEST_TOKEN_LIFETIME", defaultIngestTokenLifetime)
	key := envconfig.String("LOCO_INGEST_TRANSIT_KEY", "")
	if err := transit.ValidateKeyName(key); err != nil {
		panic(fmt.Errorf("%w: %w", errInvalidIngestKey, err))
	}
	return IngestConfig{TransitKey: key, TokenLifetime: lifetime}
}

func newAgentConfig(defaults servicedefaults.Defaults) service.AgentConfig {
	assertionLifetime := envconfig.PositiveDuration(
		"LOCO_AGENT_ASSERTION_MAX_LIFETIME",
		defaultAgentAssertionMaxLifetime,
	)
	joinTokenTTL := envconfig.PositiveDuration("LOCO_CLUSTER_JOIN_TOKEN_TTL", defaultClusterJoinTokenTTL)
	joinTokenMaxTTL := envconfig.PositiveDuration("LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL", defaultClusterJoinTokenMaxTTL)
	if joinTokenTTL > joinTokenMaxTTL {
		panic(errors.New("LOCO_CLUSTER_JOIN_TOKEN_TTL must not exceed LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL"))
	}
	return service.AgentConfig{
		JoinTokenMaxTTL:      joinTokenMaxTTL,
		ServiceDefaults:      defaults,
		AssertionMaxLifetime: assertionLifetime,
		JoinTokenTTL:         joinTokenTTL,
		ApplyAttempts:        applyAttemptsEnv(),
		ApplyRetryDelay:      positiveInt32Env("LOCO_APPLY_RETRY_DELAY", defaultApplyRetryDelay),
		WorkspaceNamespaceGrace: envconfig.PositiveDuration(
			"LOCO_WORKSPACE_NAMESPACE_GRACE",
			defaultWorkspaceNamespaceGrace,
		),
	}
}
