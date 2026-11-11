package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/pkg/transit"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/api/sweepers"
	"github.com/team-loco/loco/api/webhooks"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/logretention"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	maxReplicasEnv      = "LOCO_DEFAULT_MAX_REPLICAS"
	registryHostEnv     = "LOCO_REGISTRY_HOST"
	registryURLEnv      = "LOCO_REGISTRY_URL"
	registryUserEnv     = "LOCO_REGISTRY_USERNAME"
	registryPassEnv     = "LOCO_REGISTRY_PASSWORD"
	testRegistryHost    = "registry.loco.test"
	testRegistryUser    = "api"
	testRegistryPass    = "secret"
	retentionEnv        = "EVENTS_RETENTION"
	installHooksEnv     = "INSTALL_WEBHOOKS"
	webhookTimeoutEnv   = "WEBHOOK_TIMEOUT"
	webhookBatchEnv     = "WEBHOOK_BATCH"
	webhookRetryEnv     = "WEBHOOK_RETRY_DELAYS"
	webhookPollEnv      = "WEBHOOK_POLL_INTERVAL"
	orgLinksEnv         = "ORG_LINKS"
	authIssuersEnv      = "AUTH_ISSUERS"
	testAdminEnv        = "LOCO_TEST_ADMIN_TOKEN"
	testAdminToken      = "service-key"
	testIssuer          = "https://issuer.loco.test"
	testHookURL         = "https://hooks.loco.test/events"
	testHTTPHookURL     = "http://hooks.loco.test/events"
	testDatabaseURL     = "postgres://db.loco.test:5432/loco"
	testHookSecret      = "whsec_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	transitAddrEnv      = "LOCO_SECRETS_TRANSIT_ADDR"
	transitMountEnv     = "LOCO_SECRETS_TRANSIT_MOUNT"
	transitKeyEnv       = "LOCO_SECRETS_TRANSIT_KEY"
	transitMethodEnv    = "LOCO_SECRETS_TRANSIT_AUTH"
	transitAuthEnv      = "LOCO_SECRETS_TRANSIT_TOKEN"
	transitKubeMount    = "LOCO_SECRETS_TRANSIT_KUBERNETES_MOUNT"
	transitKubeRole     = "LOCO_SECRETS_TRANSIT_KUBERNETES_ROLE"
	transitKubeJWTFile  = "LOCO_SECRETS_TRANSIT_KUBERNETES_TOKEN_FILE"
	transitCAEnv        = "LOCO_SECRETS_TRANSIT_CA_FILE"
	transitTimeEnv      = "LOCO_SECRETS_TRANSIT_TIMEOUT"
	transitStartupEnv   = "LOCO_SECRETS_TRANSIT_STARTUP_TIMEOUT"
	transitMarginEnv    = "LOCO_SECRETS_TRANSIT_RENEW_MARGIN"
	transitRetryEnv     = "LOCO_SECRETS_TRANSIT_RENEW_RETRY"
	transitCacheEnv     = "LOCO_SECRETS_TRANSIT_CACHE_TTL"
	ingestKeyEnv        = "LOCO_INGEST_TRANSIT_KEY"
	ingestLifetimeEnv   = "LOCO_INGEST_TOKEN_LIFETIME"
	testIngestKey       = "loco-ingest"
	registryAuthEnv     = "LOCO_REGISTRY_AUTH"
	registryKeyEnv      = "LOCO_REGISTRY_TRANSIT_KEY"
	registryVersionEnv  = "LOCO_REGISTRY_TRANSIT_KEY_VERSION"
	registryServiceEnv  = "LOCO_REGISTRY_TOKEN_SERVICE"
	registryAccessEnv   = "LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME"
	registryPullEnv     = "LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME"
	registrySweepEnv    = "LOCO_REGISTRY_CREDENTIAL_SWEEP_INTERVAL"
	testRegistryService = "loco-registry-service"
	testRegistryKey     = "loco-registry"
	testTransitAddr     = "https://bao.loco.test:8200"
	testTransitKey      = "loco-dek"
	testTransitAuth     = "s.transit"
	testTransitRole     = "loco-api"
	testTransitKube     = "kubernetes"
	testTransitJWT      = "/var/run/secrets/openbao/token"
)

var adminIssuers = `[{"issuer":"` + testIssuer + `","audience":"loco",` +
	`"admin":{"type":"supabase","tokenEnv":"` + testAdminEnv + `"}}]`

func clearAPIConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOCO_CLUSTER_JOIN_TOKEN_TTL", "")
	t.Setenv("LOCO_AGENT_ASSERTION_MAX_LIFETIME", "")
	t.Setenv("DATABASE_LISTEN_URL", "")
	t.Setenv("CACHE_TYPE", "")
	t.Setenv("CACHE_ADDR", "")
	t.Setenv("LOCO_SOURCE_MAX_BYTES", "")
	t.Setenv("LOCO_SOURCE_BUCKET", "")
	t.Setenv("LOCO_SOURCE_BUCKET_REGION", "")
	t.Setenv("LOCO_SOURCE_BUCKET_ACCESS_KEY_ID", "")
	t.Setenv("LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY", "")
	t.Setenv("LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE", "")
	t.Setenv("LOCO_DEFAULT_CPU_TARGET", "")
	t.Setenv("LOCO_DEFAULT_CPU", "")
	t.Setenv("LOCO_DEFAULT_MEMORY", "")
	t.Setenv("LOCO_DEFAULT_MIN_REPLICAS", "")
	t.Setenv(maxReplicasEnv, "")
	t.Setenv("LOCO_DEFAULT_PATH_PREFIX", "")
	t.Setenv("LOCO_DEFAULT_IDLE_TIMEOUT", "")
	t.Setenv("LOCO_SCHEMA_URL", "")
	t.Setenv("LOCO_DEFAULT_PORT", "")
	t.Setenv("LOCO_DEFAULT_HEALTH_PATH", "")
	t.Setenv("LOCO_DEFAULT_HEALTH_INTERVAL", "")
	t.Setenv("LOCO_DEFAULT_HEALTH_TIMEOUT", "")
	t.Setenv("LOCO_DEFAULT_HEALTH_FAIL_THRESHOLD", "")
	t.Setenv("LOCO_DEFAULT_HEALTH_STARTUP_GRACE_PERIOD", "")
	t.Setenv("LOCO_DEFAULT_DEPLOY_ATTEMPTS", "")
	t.Setenv("LOCO_DEFAULT_DEPLOY_RETRY_DELAY", "")
	t.Setenv(registryHostEnv, "")
	t.Setenv("LOCO_REGISTRY_PREFIX", "")
	t.Setenv(registryURLEnv, "")
	t.Setenv(registryUserEnv, "")
	t.Setenv(registryPassEnv, "")
	t.Setenv("LOCO_REGISTRY_TIMEOUT", "")
	t.Setenv("LOCO_IMAGE_RETENTION", "")
	t.Setenv("LOCO_IMAGE_SWEEP_INTERVAL", "")
	t.Setenv("LOCO_IMAGE_SWEEP_BUILD_BATCH", "")
	t.Setenv("LOCO_IMAGE_SWEEP_REPOSITORY_BATCH", "")
	t.Setenv("LOCO_IMAGE_SWEEP_TAG_BATCH", "")
	t.Setenv("LOCO_IMAGE_SWEEP_TAG_MIN_AGE", "")
	t.Setenv("LOCO_SOURCE_SWEEP_INTERVAL", "")
	t.Setenv("LOCO_SOURCE_SWEEP_UPLOAD_GRACE", "")
	t.Setenv("LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE", "")
	t.Setenv("LOCO_SOURCE_SWEEP_BUILD_BATCH", "")
	t.Setenv("LOCO_SOURCE_SWEEP_ORPHAN_PAGE_SIZE", "")
	t.Setenv("LOCO_SOURCE_SWEEP_ORPHAN_MAX_PAGES", "")
	t.Setenv("WEBHOOK_ALLOW_PRIVATE_NETWORKS", "")
	t.Setenv(retentionEnv, "")
	t.Setenv("DATABASE_URL", testDatabaseURL)
	t.Setenv("DATABASE_MAX_CONNS", "")
	t.Setenv("DATABASE_MIN_CONNS", "")
	t.Setenv("DATABASE_STATEMENT_TIMEOUT", "")
	t.Setenv("LOCO_API_TOKEN_MAX_LIFETIME", "")
	t.Setenv("LOCO_SESSION_ACCESS_TOKEN_LIFETIME", "")
	t.Setenv("LOCO_SESSION_REFRESH_TOKEN_LIFETIME", "")
	t.Setenv("LOCO_TOKEN_LAST_USED_INTERVAL", "")
	t.Setenv("LOCO_TOKEN_CLEANUP_INTERVAL", "")
	t.Setenv("LOCO_IMAGE_RESOLVE_TIMEOUT", "")
	t.Setenv("LOCO_RPC_TIMEOUT", "")
	t.Setenv("LOCO_SHUTDOWN_DRAIN_TIMEOUT", "")
	t.Setenv("LOCO_PLACEMENT_POLL_INTERVAL", "")
	t.Setenv(webhookTimeoutEnv, "")
	t.Setenv(webhookBatchEnv, "")
	t.Setenv(webhookRetryEnv, "")
	t.Setenv(webhookPollEnv, "")
	t.Setenv(installHooksEnv, "")
	t.Setenv(orgLinksEnv, "")
	t.Setenv(authIssuersEnv, "")
	t.Setenv("AUTH_SIGNUP_MODE", "")
	t.Setenv("MIN_CLI_VERSION", "")
	t.Setenv(testAdminEnv, "")
	t.Setenv(transitAddrEnv, testTransitAddr)
	t.Setenv(transitMountEnv, "")
	t.Setenv(transitKeyEnv, testTransitKey)
	t.Setenv(transitMethodEnv, transit.AuthToken)
	t.Setenv(transitAuthEnv, testTransitAuth)
	t.Setenv(transitKubeMount, "")
	t.Setenv(transitKubeRole, "")
	t.Setenv(transitKubeJWTFile, "")
	t.Setenv(transitCAEnv, "")
	t.Setenv(transitTimeEnv, "")
	t.Setenv(transitStartupEnv, "")
	t.Setenv(transitMarginEnv, "")
	t.Setenv(transitRetryEnv, "")
	t.Setenv(transitCacheEnv, "")
	t.Setenv(ingestKeyEnv, testIngestKey)
	t.Setenv(ingestLifetimeEnv, "")
	t.Setenv(registryAuthEnv, "")
	t.Setenv(registryKeyEnv, "")
	t.Setenv(registryVersionEnv, "")
	t.Setenv(registryServiceEnv, "")
	t.Setenv(registryAccessEnv, "")
	t.Setenv(registryPullEnv, "")
	t.Setenv(registrySweepEnv, "")
	t.Setenv("LOCO_SECRETS_MAX_VALUE_BYTES", "")
	t.Setenv("LOCO_SECRETS_MAX_PER_SERVICE", "")
	t.Setenv("LOCO_SECRETS_MAX_PLACEMENT_BYTES", "")
	t.Setenv("LOCO_SECRETS_LOCK_TIMEOUT", "")
	t.Setenv(bootstrapEnv, "")
	t.Setenv("INVITATION_TTL", "")
	t.Setenv(otlpEndpointEnv, "")
	t.Setenv(otlpTracesEndpointEnv, "")
}

func TestNewAPIConfigReadsBootstrapToken(t *testing.T) {
	clearAPIConfigEnv(t)
	if ac := newAPIConfig(); ac.BootstrapTokenHash != nil {
		t.Fatalf("bootstrap token hash = %x without a token", ac.BootstrapTokenHash)
	}
	token := bearer.New("")
	t.Setenv(bootstrapEnv, token)
	ac := newAPIConfig()
	if !slices.Equal(ac.BootstrapTokenHash, service.HashBootstrapToken(token)) {
		t.Fatalf("bootstrap token hash = %x", ac.BootstrapTokenHash)
	}
}

func TestNewAPIConfigDefaults(t *testing.T) {
	clearAPIConfigEnv(t)
	ac := newAPIConfig()
	if ac.CacheType != cacheTypeMemory {
		t.Errorf("cache type = %q, want %q", ac.CacheType, cacheTypeMemory)
	}
	if ac.SourceMaxBytes != defaultSourceMaxBytes {
		t.Errorf("source max bytes = %d, want %d", ac.SourceMaxBytes, defaultSourceMaxBytes)
	}
	if ac.SchemaURL != defaultSchemaURL {
		t.Errorf("schema url = %q, want %q", ac.SchemaURL, defaultSchemaURL)
	}
	want := servicedefaults.Defaults{
		CPU:                      defaultServiceCPU,
		Memory:                   defaultServiceMemory,
		MinReplicas:              defaultServiceMinReplicas,
		MaxReplicas:              defaultServiceMaxReplicas,
		CPUTarget:                defaultServiceCPUTarget,
		PathPrefix:               defaultServicePathPrefix,
		IdleTimeout:              defaultServiceIdleTimeout,
		Port:                     defaultServicePort,
		HealthPath:               defaultHealthPath,
		HealthInterval:           defaultHealthInterval,
		HealthTimeout:            defaultHealthTimeout,
		HealthFailThreshold:      defaultHealthFailThresh,
		HealthStartupGracePeriod: defaultHealthGracePeriod,
		DeployAttempts:           defaultDeployAttempts,
		DeployRetryDelay:         defaultDeployRetryDelay,
		MetricsPort:              defaultMetricsPort,
		MetricsPath:              defaultMetricsPath,
		LogRetention:             logretention.Maximum(),
	}
	if ac.ServiceDefaults != want {
		t.Errorf("service defaults = %+v, want %+v", ac.ServiceDefaults, want)
	}
	wantSweep := sweepers.ImageSweepConfig{
		Retention:       defaultImageRetention,
		Interval:        defaultImageSweepInterval,
		BuildBatch:      defaultImageSweepBuildBatch,
		RepositoryBatch: defaultImageSweepRepositoryBatch,
		TagBatch:        defaultImageSweepTagBatch,
		TagMinAge:       defaultImageSweepTagMinAge,
	}
	if ac.ImageSweep != wantSweep {
		t.Errorf("image sweep = %+v, want %+v", ac.ImageSweep, wantSweep)
	}
	wantSource := sweepers.SourceSweepConfig{
		Interval:       defaultSourceSweepInterval,
		UploadGrace:    defaultSourceUploadGrace,
		OrphanMinAge:   defaultSourceOrphanMinAge,
		BuildBatch:     defaultSourceSweepBuildBatch,
		OrphanPageSize: defaultSourceOrphanPageSize,
		OrphanMaxPages: defaultSourceOrphanMaxPages,
	}
	if ac.SourceSweep != wantSource {
		t.Errorf("source sweep = %+v, want %+v", ac.SourceSweep, wantSource)
	}
	if ac.Registry.Username != "" {
		t.Errorf("registry username = %q, want none so image cleanup is off", ac.Registry.Username)
	}
	if ac.EventsRetention != defaultEventsRetention {
		t.Errorf("events retention = %v, want %v", ac.EventsRetention, defaultEventsRetention)
	}
	if len(ac.InstallWebhooks) != 0 || len(ac.AuthIssuers) != 0 {
		t.Errorf("install webhooks = %v, issuers = %v, want none", ac.InstallWebhooks, ac.AuthIssuers)
	}
	if ac.SignupPolicy.Closed {
		t.Errorf("signup policy = %+v, want open", ac.SignupPolicy)
	}
	wantSecrets := service.SecretConfig{
		MaxValueBytes:     defaultSecretMaxValueBytes,
		MaxPerService:     defaultSecretMaxPerService,
		MaxPlacementBytes: defaultSecretMaxPlacementBytes,
		LockTimeout:       defaultSecretLockTimeout,
	}
	if ac.SecretLimits != wantSecrets {
		t.Errorf("secret limits = %+v, want %+v", ac.SecretLimits, wantSecrets)
	}
}

func TestNewAPIConfigReadsSecretLimits(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_SECRETS_MAX_VALUE_BYTES", "1024")
	t.Setenv("LOCO_SECRETS_MAX_PER_SERVICE", "8")
	t.Setenv("LOCO_SECRETS_MAX_PLACEMENT_BYTES", "4096")
	t.Setenv("LOCO_SECRETS_LOCK_TIMEOUT", "250ms")
	limits := newAPIConfig().SecretLimits
	want := service.SecretConfig{
		MaxValueBytes:     1024,
		MaxPerService:     8,
		MaxPlacementBytes: 4096,
		LockTimeout:       250 * time.Millisecond,
	}
	if limits != want {
		t.Errorf("secret limits = %+v, want %+v", limits, want)
	}
	for _, name := range []string{
		"LOCO_SECRETS_MAX_PER_SERVICE",
		"LOCO_SECRETS_MAX_PLACEMENT_BYTES",
		"LOCO_SECRETS_LOCK_TIMEOUT",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "0")
			err := panicValue(t, func() { newAPIConfig() })
			if !errors.Is(err, envconfig.ErrNotPositive) {
				t.Errorf("panic = %v, want %v", err, envconfig.ErrNotPositive)
			}
		})
	}
}

func TestNewAPIConfigReadsEventsRetention(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(retentionEnv, "168h")
	ac := newAPIConfig()
	if ac.EventsRetention != 168*time.Hour {
		t.Errorf("events retention = %v, want 168h", ac.EventsRetention)
	}
}

func TestNewAPIConfigPanicsWithoutDatabaseURL(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("DATABASE_URL", "")
	if err := panicValue(t, func() { newAPIConfig() }); !errors.Is(err, errDatabaseURLMissing) {
		t.Fatalf("panic = %v, want %v", err, errDatabaseURLMissing)
	}
}

func TestNewAPIConfigReadsWebhookDelivery(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(webhookTimeoutEnv, "3s")
	t.Setenv(webhookBatchEnv, "7")
	t.Setenv(webhookRetryEnv, "1s, 1m")
	t.Setenv(webhookPollEnv, "500ms")
	got := newAPIConfig().Webhooks
	want := webhooks.Config{
		RequestTimeout: 3 * time.Second,
		ClaimBatch:     7,
		RetryDelays:    []time.Duration{time.Second, time.Minute},
		PollInterval:   500 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("webhooks = %+v, want %+v", got, want)
	}
}

func TestNewAPIConfigReadsInstallWebhooks(t *testing.T) {
	clearAPIConfigEnv(t)
	hooks := `[{"url":"` + testHookURL + `","secret":"` + testHookSecret + `","eventTypes":["deployment.ready"]}]`
	t.Setenv(installHooksEnv, hooks)
	ac := newAPIConfig()
	if len(ac.InstallWebhooks) != 1 {
		t.Fatalf("install webhooks = %+v, want one", ac.InstallWebhooks)
	}
	hook := ac.InstallWebhooks[0]
	wantTypes := []string{"deployment.ready"}
	if hook.URL != testHookURL || hook.Secret != testHookSecret || !slices.Equal(hook.EventTypes, wantTypes) {
		t.Errorf("install webhook = %+v", hook)
	}
}

func TestNewAPIConfigReadsOrgLinks(t *testing.T) {
	clearAPIConfigEnv(t)
	if ac := newAPIConfig(); len(ac.OrgLinks) != 0 {
		t.Fatalf("org links = %+v without %s", ac.OrgLinks, orgLinksEnv)
	}
	t.Setenv(orgLinksEnv, `[{"label":"Support","url":"https://support.loco.test/orgs/{orgId}"}]`)
	ac := newAPIConfig()
	want := []service.OrgLink{{Label: "Support", URL: "https://support.loco.test/orgs/{orgId}"}}
	if !slices.Equal(ac.OrgLinks, want) {
		t.Errorf("org links = %+v, want %+v", ac.OrgLinks, want)
	}
}

func TestNewAPIConfigResolvesAdminTokens(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(authIssuersEnv, adminIssuers)
	t.Setenv(testAdminEnv, testAdminToken)
	t.Setenv("AUTH_SIGNUP_MODE", "closed")
	t.Setenv("MIN_CLI_VERSION", "v0.0.61")
	ac := newAPIConfig()
	if len(ac.AuthIssuers) != 1 || ac.AuthIssuers[0].Admin == nil {
		t.Fatalf("issuers = %+v, want one with an admin", ac.AuthIssuers)
	}
	if got := ac.AuthIssuers[0].Admin.Token; got != testAdminToken {
		t.Errorf("admin token = %q, want %q", got, testAdminToken)
	}
	if !ac.SignupPolicy.Closed {
		t.Errorf("signup policy = %+v", ac.SignupPolicy)
	}
}

func TestNewAPIConfigReadsRegistryCleanup(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv("LOCO_REGISTRY_PREFIX", "builds")
	t.Setenv(registryUserEnv, testRegistryUser)
	t.Setenv(registryPassEnv, testRegistryPass)
	t.Setenv("LOCO_IMAGE_RETENTION", "2")
	t.Setenv("LOCO_IMAGE_SWEEP_INTERVAL", "30s")
	t.Setenv("LOCO_IMAGE_SWEEP_BUILD_BATCH", "10")
	t.Setenv("LOCO_IMAGE_SWEEP_REPOSITORY_BATCH", "20")
	t.Setenv("LOCO_IMAGE_SWEEP_TAG_BATCH", "30")
	t.Setenv("LOCO_IMAGE_SWEEP_TAG_MIN_AGE", "2h")
	ac := newAPIConfig()
	wantRegistry := registryclient.Config{
		URL:      "https://" + testRegistryHost,
		Username: testRegistryUser,
		Password: testRegistryPass,
		Timeout:  defaultRegistryTimeout,
	}
	if !reflect.DeepEqual(ac.Registry, wantRegistry) {
		t.Errorf("registry = %+v, want %+v", ac.Registry, wantRegistry)
	}
	wantSweep := sweepers.ImageSweepConfig{
		RegistryHost:    testRegistryHost,
		RegistryPrefix:  "builds",
		Retention:       2,
		Interval:        30 * time.Second,
		BuildBatch:      10,
		RepositoryBatch: 20,
		TagBatch:        30,
		TagMinAge:       2 * time.Hour,
	}
	if ac.ImageSweep != wantSweep {
		t.Errorf("image sweep = %+v, want %+v", ac.ImageSweep, wantSweep)
	}

	t.Setenv(registryURLEnv, "http://localhost:5001")
	t.Setenv("LOCO_REGISTRY_TIMEOUT", "5s")
	ac = newAPIConfig()
	if ac.Registry.URL != "http://localhost:5001" || ac.Registry.Timeout != 5*time.Second {
		t.Errorf("registry = %+v, want the overridden url and timeout", ac.Registry)
	}
}

func TestImageRetentionZeroDisablesCleanup(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv(registryUserEnv, testRegistryUser)
	t.Setenv(registryPassEnv, testRegistryPass)
	t.Setenv("LOCO_IMAGE_RETENTION", "0")
	ac := newAPIConfig()
	if ac.ImageSweep.Retention != imageRetentionDisabled {
		t.Fatalf("retention = %d, want %d", ac.ImageSweep.Retention, imageRetentionDisabled)
	}
	disabled, err := newImageRegistry(ac.Registry, ac.ImageSweep.Retention)
	if err != nil {
		t.Fatalf("new image registry: %v", err)
	}
	if disabled != nil {
		t.Fatal("retention 0 created a registry client, so the image sweeper would start")
	}

	t.Setenv("LOCO_IMAGE_RETENTION", "1")
	ac = newAPIConfig()
	enabled, err := newImageRegistry(ac.Registry, ac.ImageSweep.Retention)
	if err != nil {
		t.Fatalf("new image registry: %v", err)
	}
	if enabled == nil {
		t.Fatal("retention 1 with credentials created no registry client")
	}
}

func TestTokenModeImageRegistryNeedsNoCredentials(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryAuthEnv, locoControllerV1.RegistryAuthToken)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv(registryKeyEnv, testRegistryKey)
	t.Setenv(registryVersionEnv, "1")
	t.Setenv(registryServiceEnv, testRegistryService)
	ac := newAPIConfig()
	without, err := newImageRegistry(ac.Registry, ac.ImageSweep.Retention)
	if err != nil {
		t.Fatalf("new image registry: %v", err)
	}
	if without != nil {
		t.Fatal("a registry without credentials or a token source created a registry client")
	}
	tokenConfig := ac.Registry
	tokenConfig.TokenSource = func(context.Context, []registrytoken.Access) (string, error) {
		return "", nil
	}
	registry, err := newImageRegistry(tokenConfig, ac.ImageSweep.Retention)
	if err != nil {
		t.Fatalf("new image registry: %v", err)
	}
	if registry == nil {
		t.Fatal("token mode without a registry username created no registry client")
	}
}

func TestNewAPIConfigReadsServiceDefaults(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_DEFAULT_CPU_TARGET", "65")
	t.Setenv("LOCO_DEFAULT_CPU", "250m")
	t.Setenv("LOCO_DEFAULT_MEMORY", "512Mi")
	t.Setenv("LOCO_DEFAULT_MIN_REPLICAS", "2")
	t.Setenv(maxReplicasEnv, "4")
	t.Setenv("LOCO_DEFAULT_PATH_PREFIX", "/app")
	t.Setenv("LOCO_DEFAULT_IDLE_TIMEOUT", "120")
	t.Setenv("LOCO_DEFAULT_PORT", "3000")
	t.Setenv("LOCO_DEFAULT_HEALTH_PATH", "/healthz")
	t.Setenv("LOCO_DEFAULT_HEALTH_INTERVAL", "10")
	t.Setenv("LOCO_DEFAULT_HEALTH_TIMEOUT", "2")
	t.Setenv("LOCO_DEFAULT_HEALTH_FAIL_THRESHOLD", "5")
	t.Setenv("LOCO_DEFAULT_HEALTH_STARTUP_GRACE_PERIOD", "15")
	t.Setenv("LOCO_DEFAULT_DEPLOY_ATTEMPTS", "4")
	t.Setenv("LOCO_DEFAULT_DEPLOY_RETRY_DELAY", "60")
	ac := newAPIConfig()
	want := servicedefaults.Defaults{
		CPU:                      "250m",
		CPUTarget:                65,
		Memory:                   "512Mi",
		MinReplicas:              2,
		MaxReplicas:              4,
		PathPrefix:               "/app",
		IdleTimeout:              120,
		Port:                     3000,
		HealthPath:               "/healthz",
		HealthInterval:           10,
		HealthTimeout:            2,
		HealthFailThreshold:      5,
		HealthStartupGracePeriod: 15,
		DeployAttempts:           4,
		DeployRetryDelay:         60,
		MetricsPort:              defaultMetricsPort,
		MetricsPath:              defaultMetricsPath,
		LogRetention:             logretention.Maximum(),
	}
	if ac.ServiceDefaults != want {
		t.Errorf("service defaults = %+v, want %+v", ac.ServiceDefaults, want)
	}
	if ac.Agent.DeployAttempts != 4 || ac.Agent.DeployRetryDelay != 60 {
		t.Errorf(
			"agent deploy policy = %d attempts, %ds apart; want 4, 60s",
			ac.Agent.DeployAttempts,
			ac.Agent.DeployRetryDelay,
		)
	}
}

func TestNewAPIConfigPanicsOnInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want error
	}{
		{"valkey without address", map[string]string{"CACHE_TYPE": cacheTypeValkey}, errCacheAddrMissing},
		{"unknown cache type", map[string]string{"CACHE_TYPE": "redis"}, errUnknownCacheType},
		{"non-numeric source max bytes", map[string]string{"LOCO_SOURCE_MAX_BYTES": "lots"}, errInvalidSourceBytes},
		{"zero source max bytes", map[string]string{"LOCO_SOURCE_MAX_BYTES": "0"}, errInvalidSourceBytes},
		{
			"non-boolean force path style",
			map[string]string{"LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE": "sometimes"},
			envconfig.ErrInvalidBool,
		},
		{
			"non-boolean webhook private networks",
			map[string]string{"WEBHOOK_ALLOW_PRIVATE_NETWORKS": "sometimes"},
			envconfig.ErrInvalidBool,
		},
		{"invalid default cpu", map[string]string{"LOCO_DEFAULT_CPU": "lots"}, errInvalidServiceDefault},
		{"invalid default memory", map[string]string{"LOCO_DEFAULT_MEMORY": "-1Gi"}, errInvalidServiceDefault},
		{"zero default replicas", map[string]string{"LOCO_DEFAULT_MIN_REPLICAS": "0"}, errInvalidServiceDefault},
		{"non-numeric default replicas", map[string]string{maxReplicasEnv: "many"}, envconfig.ErrInvalidInt32},
		{
			"default max below min",
			map[string]string{"LOCO_DEFAULT_MIN_REPLICAS": "3", maxReplicasEnv: "2"},
			errInvalidServiceDefault,
		},
		{
			"relative default path prefix",
			map[string]string{"LOCO_DEFAULT_PATH_PREFIX": "app"},
			errInvalidServiceDefault,
		},
		{"zero default idle timeout", map[string]string{"LOCO_DEFAULT_IDLE_TIMEOUT": "0"}, errInvalidServiceDefault},
		{
			"default cpu below the controller minimum",
			map[string]string{"LOCO_DEFAULT_CPU": "50m"},
			errInvalidServiceDefault,
		},
		{
			"default memory above the controller maximum",
			map[string]string{"LOCO_DEFAULT_MEMORY": "8Gi"},
			errInvalidServiceDefault,
		},
		{
			"default max replicas above the controller maximum",
			map[string]string{maxReplicasEnv: "12"},
			errInvalidServiceDefault,
		},
		{"negative image retention", map[string]string{"LOCO_IMAGE_RETENTION": "-1"}, errNegative},
		{"non-numeric image retention", map[string]string{"LOCO_IMAGE_RETENTION": "all"}, envconfig.ErrInvalidInt32},
		{
			"non-duration sweep interval",
			map[string]string{"LOCO_IMAGE_SWEEP_INTERVAL": "10"},
			envconfig.ErrInvalidDuration,
		},
		{"negative sweep interval", map[string]string{"LOCO_IMAGE_SWEEP_INTERVAL": "-1m"}, envconfig.ErrNotPositive},
		{"zero build batch", map[string]string{"LOCO_IMAGE_SWEEP_BUILD_BATCH": "0"}, envconfig.ErrNotPositive},
		{
			"zero repository batch",
			map[string]string{"LOCO_IMAGE_SWEEP_REPOSITORY_BATCH": "0"},
			envconfig.ErrNotPositive,
		},
		{"zero tag batch", map[string]string{"LOCO_IMAGE_SWEEP_TAG_BATCH": "0"}, envconfig.ErrNotPositive},
		{"zero tag min age", map[string]string{"LOCO_IMAGE_SWEEP_TAG_MIN_AGE": "0s"}, envconfig.ErrNotPositive},
		{
			"non-duration source sweep interval",
			map[string]string{"LOCO_SOURCE_SWEEP_INTERVAL": "5"},
			envconfig.ErrInvalidDuration,
		},
		{"zero upload grace", map[string]string{"LOCO_SOURCE_SWEEP_UPLOAD_GRACE": "0s"}, envconfig.ErrNotPositive},
		{
			"negative orphan min age",
			map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE": "-1h"},
			envconfig.ErrNotPositive,
		},
		{"zero source build batch", map[string]string{"LOCO_SOURCE_SWEEP_BUILD_BATCH": "0"}, envconfig.ErrNotPositive},
		{
			"zero orphan page size",
			map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_PAGE_SIZE": "0"},
			envconfig.ErrNotPositive,
		},
		{
			"non-numeric orphan max pages",
			map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_MAX_PAGES": "x"},
			envconfig.ErrInvalidInt32,
		},
		{"zero registry timeout", map[string]string{"LOCO_REGISTRY_TIMEOUT": "0s"}, envconfig.ErrNotPositive},
		{"zero events retention", map[string]string{retentionEnv: "0s"}, envconfig.ErrNotPositive},
		{"non-duration events retention", map[string]string{retentionEnv: "90"}, envconfig.ErrInvalidDuration},
		{"zero webhook retry delay", map[string]string{webhookRetryEnv: "30s,0s"}, envconfig.ErrNotPositive},
		{
			"non-duration webhook retry delay",
			map[string]string{webhookRetryEnv: "30s,soon"},
			envconfig.ErrInvalidDuration,
		},
		{
			"database min conns above max",
			map[string]string{"DATABASE_MIN_CONNS": "10", "DATABASE_MAX_CONNS": "5"},
			errDatabasePoolSize,
		},
		{"malformed install webhooks", map[string]string{installHooksEnv: "{"}, errInvalidInstallWebhooks},
		{
			"install webhook without a secret",
			map[string]string{installHooksEnv: `[{"url":"` + testHookURL + `"}]`},
			errInvalidInstallWebhooks,
		},
		{
			"org link with an unknown placeholder",
			map[string]string{orgLinksEnv: `[{"label":"Support","url":"https://support.loco.test/{userId}"}]`},
			errInvalidOrgLinks,
		},
		{
			"short bootstrap token",
			map[string]string{bootstrapEnv: bearer.New("")[:minBootstrapTokenLength-1]},
			errShortBootstrapToken,
		},
		{"malformed auth issuers", map[string]string{authIssuersEnv: "["}, errInvalidAuthIssuers},
		{"admin token env unset", map[string]string{authIssuersEnv: adminIssuers}, errAdminTokenMissing},
		{"non-semver min cli version", map[string]string{"MIN_CLI_VERSION": "0.0.61"}, errInvalidMinCLIVersion},
		{
			"registry username without password",
			map[string]string{registryHostEnv: testRegistryHost, registryUserEnv: testRegistryUser},
			errRegistryAuthPartial,
		},
		{
			"registry password without username",
			map[string]string{registryHostEnv: testRegistryHost, registryPassEnv: testRegistryPass},
			errRegistryAuthPartial,
		},
		{
			"registry credentials without host",
			map[string]string{registryUserEnv: testRegistryUser, registryPassEnv: testRegistryPass},
			errRegistryAuthNoHost,
		},
		{
			"registry url with a path",
			map[string]string{
				registryHostEnv: testRegistryHost,
				registryUserEnv: testRegistryUser,
				registryPassEnv: testRegistryPass,
				registryURLEnv:  "https://registry.loco.test/v2",
			},
			errInvalidRegistry,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			err := panicValue(t, func() { newAPIConfig() })
			if !errors.Is(err, tt.want) {
				t.Errorf("panic = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewAPIConfigPanicsWithoutTransit(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(transitAddrEnv, "")
	t.Setenv(transitKeyEnv, "")
	t.Setenv(transitAuthEnv, "")
	t.Setenv(transitMountEnv, "kms/transit")
	err := panicValue(t, func() { newAPIConfig() })
	if !errors.Is(err, errInvalidSecretsTransit) {
		t.Errorf("panic = %v, want %v", err, errInvalidSecretsTransit)
	}
}

func TestNewAPIConfigReadsTransitProvider(t *testing.T) {
	clearAPIConfigEnv(t)
	ac := newAPIConfig()
	got := ac.Transit
	want := transit.Config{
		Address:        testTransitAddr,
		Mount:          defaultSecretsTransitMount,
		Auth:           transit.AuthToken,
		Token:          testTransitAuth,
		Timeout:        defaultSecretsTransitTimeout,
		StartupTimeout: defaultSecretsTransitStartup,
		RenewMargin:    defaultSecretsTransitMargin,
		RenewRetry:     defaultSecretsTransitRetry,
	}
	if got != want {
		t.Errorf("transit config = %+v, want %+v", got, want)
	}
	wantSecrets := secretkeys.Config{Key: testTransitKey, CacheTTL: defaultSecretsTransitDEKTTL}
	if ac.Secrets != wantSecrets {
		t.Errorf("secrets config = %+v, want %+v", ac.Secrets, wantSecrets)
	}

	t.Setenv(transitMountEnv, "kms/transit")
	t.Setenv(transitCAEnv, "/etc/loco/bao-ca.pem")
	t.Setenv(transitTimeEnv, "3s")
	t.Setenv(transitStartupEnv, "45s")
	t.Setenv(transitMarginEnv, "1m")
	t.Setenv(transitRetryEnv, "2s")
	t.Setenv(transitCacheEnv, "30s")
	ac = newAPIConfig()
	got = ac.Transit
	want.Mount = "kms/transit"
	want.CAFile = "/etc/loco/bao-ca.pem"
	want.Timeout = 3 * time.Second
	want.StartupTimeout = 45 * time.Second
	want.RenewMargin = time.Minute
	want.RenewRetry = 2 * time.Second
	wantSecrets.CacheTTL = 30 * time.Second
	if got != want {
		t.Errorf("transit config = %+v, want %+v", got, want)
	}
	if ac.Secrets != wantSecrets {
		t.Errorf("secrets config = %+v, want %+v", ac.Secrets, wantSecrets)
	}
}

func TestNewAPIConfigReadsTransitKubernetesAuth(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(transitMethodEnv, transit.AuthKubernetes)
	t.Setenv(transitAuthEnv, "")
	t.Setenv(transitKubeMount, testTransitKube)
	t.Setenv(transitKubeRole, testTransitRole)
	t.Setenv(transitKubeJWTFile, testTransitJWT)
	got := newAPIConfig().Transit
	want := transit.KubernetesConfig{Mount: testTransitKube, Role: testTransitRole, TokenFile: testTransitJWT}
	if got.Auth != transit.AuthKubernetes || got.Token != "" || got.Kubernetes != want {
		t.Errorf("transit config = %+v, want kubernetes auth with %+v", got, want)
	}
}

func TestNewAPIConfigPanicsOnPartialTransit(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want error
	}{
		{
			"transit without an address",
			map[string]string{
				transitAddrEnv: "",
			},
			transit.ErrAddress,
		},
		{
			"transit without a token",
			map[string]string{
				transitAuthEnv: "",
			},
			transit.ErrToken,
		},
		{
			"transit without an auth method",
			map[string]string{
				transitMethodEnv: "",
			},
			transit.ErrAuth,
		},
		{
			"kubernetes auth without a role",
			map[string]string{
				transitMethodEnv:   transit.AuthKubernetes,
				transitAuthEnv:     "",
				transitKubeMount:   testTransitKube,
				transitKubeJWTFile: testTransitJWT,
			},
			transit.ErrKubernetes,
		},
		{
			"kubernetes auth with a token",
			map[string]string{
				transitMethodEnv:   transit.AuthKubernetes,
				transitKubeMount:   testTransitKube,
				transitKubeRole:    testTransitRole,
				transitKubeJWTFile: testTransitJWT,
			},
			transit.ErrKubernetes,
		},
		{
			"transit without a key",
			map[string]string{
				transitKeyEnv: "",
			},
			transit.ErrKeyName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			err := panicValue(t, func() { newAPIConfig() })
			if !errors.Is(err, tt.want) {
				t.Errorf("panic = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewAPIConfigPanicsOnPartialSourceBucket(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_SOURCE_BUCKET", "loco-sources")
	err := panicValue(t, func() { newAPIConfig() })
	if !strings.Contains(err.Error(), "source bucket") {
		t.Errorf("panic = %v, want a source bucket error", err)
	}
}

func TestNewAPIConfigReadsSchemaURL(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_SCHEMA_URL", "https://loco.test/schemas/loco.v1.json")
	ac := newAPIConfig()
	if ac.SchemaURL != "https://loco.test/schemas/loco.v1.json" {
		t.Errorf("schema url = %q, want the configured value", ac.SchemaURL)
	}
}

func TestDatabaseListenURLDefaultsAndOverride(t *testing.T) {
	t.Setenv("DATABASE_LISTEN_URL", "")
	fallback := newDatabaseListenConfig(testDatabaseURL)
	if fallback.Host != "db.loco.test" {
		t.Fatalf("default listener host = %s", fallback.Host)
	}
	const directURL = "postgres://direct.loco.test:5432/loco"
	t.Setenv("DATABASE_LISTEN_URL", directURL)
	direct := newDatabaseListenConfig(testDatabaseURL)
	if direct.Host != "direct.loco.test" {
		t.Fatalf("listener host = %s, want direct.loco.test", direct.Host)
	}
	t.Setenv("DATABASE_LISTEN_URL", "://invalid")
	if err := panicValue(t, func() { newDatabaseListenConfig(testDatabaseURL) }); err == nil {
		t.Fatal("invalid listener URL accepted")
	}
}

func TestDeployPolicyDefaultBounds(t *testing.T) {
	clearAPIConfigEnv(t)
	for _, tc := range []struct {
		env      string
		accepted []string
		rejected []string
	}{
		{"LOCO_DEFAULT_DEPLOY_ATTEMPTS", []string{"1", "10"}, []string{"0", "11", "-1", "5m"}},
		{"LOCO_DEFAULT_DEPLOY_RETRY_DELAY", []string{"60"}, []string{"59", "0", "5m"}},
	} {
		for _, value := range tc.accepted {
			t.Setenv(tc.env, value)
			newServiceDefaults()
		}
		for _, value := range tc.rejected {
			t.Setenv(tc.env, value)
			if err := panicValue(t, func() { newServiceDefaults() }); err == nil {
				t.Fatalf("%s=%s accepted", tc.env, value)
			}
		}
		t.Setenv(tc.env, "")
	}
}

func TestClusterEnrollmentDurations(t *testing.T) {
	for _, tc := range []struct {
		env      string
		fallback time.Duration
		read     func(service.AgentConfig) time.Duration
	}{
		{"LOCO_CLUSTER_JOIN_TOKEN_TTL", defaultClusterJoinTokenTTL, func(c service.AgentConfig) time.Duration {
			return c.JoinTokenTTL
		}},
		{"LOCO_AGENT_ASSERTION_MAX_LIFETIME", defaultAgentAssertionMaxLifetime, func(c service.AgentConfig) time.Duration {
			return c.AssertionMaxLifetime
		}},
	} {
		t.Setenv(tc.env, "")
		if got := tc.read(newAgentConfig(servicedefaults.Defaults{})); got != tc.fallback {
			t.Fatalf("%s default = %v, want %v", tc.env, got, tc.fallback)
		}
		t.Setenv(tc.env, "10m")
		if got := tc.read(newAgentConfig(servicedefaults.Defaults{})); got != 10*time.Minute {
			t.Fatalf("%s = %v, want 10m", tc.env, got)
		}
		t.Setenv(tc.env, "0s")
		if err := panicValue(t, func() { newAgentConfig(servicedefaults.Defaults{}) }); err == nil {
			t.Fatalf("%s accepted 0s", tc.env)
		}
		t.Setenv(tc.env, "")
	}
}

func TestNewAPIConfigReadsInvitations(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("WEB_URL", "https://app.loco.test")
	ac := newAPIConfig()
	if ac.Invitations.TTL != defaultInvitationTTL || ac.Invitations.WebURL != "https://app.loco.test" {
		t.Fatalf("invitations = %+v", ac.Invitations)
	}
	t.Setenv("INVITATION_TTL", "24h")
	if got := newAPIConfig().Invitations.TTL; got != 24*time.Hour {
		t.Fatalf("ttl = %v", got)
	}
	t.Setenv("INVITATION_TTL", "-1h")
	if err := panicValue(t, func() { newAPIConfig() }); err == nil {
		t.Fatal("negative ttl did not panic")
	}
}

func TestInvitationConfigUsesOnlyTTLAndWebURL(t *testing.T) {
	for _, name := range []string{"EMAIL_TRANSPORT", "EMAIL_HOOK_SECRET", "EMAIL_TIMEOUT", "EMAIL_FROM"} {
		t.Setenv(name, "invalid")
	}
	t.Setenv("INVITATION_TTL", "24h")
	got := newInvitationConfig("https://app.loco.test")
	if got.TTL != 24*time.Hour || got.WebURL != "https://app.loco.test" {
		t.Fatalf("invitations = %+v", got)
	}
}

func TestIngestTokenConfiguration(t *testing.T) {
	clearAPIConfigEnv(t)
	defaults := newAPIConfig().Ingest
	if defaults.TransitKey != testIngestKey || defaults.TokenLifetime != defaultIngestTokenLifetime {
		t.Fatalf("defaults = %+v", defaults)
	}
	t.Setenv(ingestLifetimeEnv, "15m")
	if set := newIngestConfig(); set.TokenLifetime != 15*time.Minute {
		t.Fatalf("configured = %+v", set)
	}
	for env, value := range map[string]string{
		ingestKeyEnv:      "",
		ingestLifetimeEnv: "0s",
	} {
		t.Run(env+"="+value, func(t *testing.T) {
			t.Setenv(env, value)
			if err := panicValue(t, func() { newAPIConfig() }); err == nil {
				t.Fatalf("invalid %s %q accepted", env, value)
			}
		})
	}
	t.Setenv(ingestLifetimeEnv, "")
	t.Setenv(ingestKeyEnv, "transit/"+testIngestKey)
	if err := panicValue(t, func() { newIngestConfig() }); !errors.Is(err, transit.ErrKeyName) {
		t.Fatalf("key name with a slash = %v", err)
	}
}

func TestRegistryAuthDefaultsToStatic(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryKeyEnv, testRegistryKey)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv(registryUserEnv, testRegistryUser)
	t.Setenv(registryPassEnv, testRegistryPass)
	ac := newAPIConfig()
	want := RegistryAuthConfig{Mode: locoControllerV1.RegistryAuthStatic}
	if ac.RegistryAuth != want {
		t.Fatalf("registry auth = %+v, want %+v", ac.RegistryAuth, want)
	}
	if ac.Agent.RegistryTokenMode {
		t.Fatal("static registry auth sends builds registry credentials")
	}
	if ac.Registry.Username != testRegistryUser || ac.Registry.Password != testRegistryPass {
		t.Fatalf("registry = %+v, want the static credentials", ac.Registry)
	}
}

func TestRegistryAuthTokenPinsTheTransitKeyVersion(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryAuthEnv, locoControllerV1.RegistryAuthToken)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv(registryKeyEnv, testRegistryKey)
	t.Setenv(registryVersionEnv, "3")
	t.Setenv(registryServiceEnv, testRegistryService)
	want := RegistryAuthConfig{
		Mode:                   locoControllerV1.RegistryAuthToken,
		TransitKey:             transit.PinnedKey{Name: testRegistryKey, Version: 3},
		Service:                testRegistryService,
		AccessTokenLifetime:    5 * time.Minute,
		PullCredentialLifetime: 24 * time.Hour,
		CredentialSweep:        10 * time.Minute,
	}
	ac := newAPIConfig()
	if ac.RegistryAuth != want {
		t.Fatalf("registry auth = %+v, want %+v", ac.RegistryAuth, want)
	}
	if !ac.Agent.RegistryTokenMode {
		t.Fatal("token registry auth sends builds no registry credentials")
	}
}

func TestRegistryAuthTokenReadsTheLifetimes(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(registryAuthEnv, locoControllerV1.RegistryAuthToken)
	t.Setenv(registryHostEnv, testRegistryHost)
	t.Setenv(registryKeyEnv, testRegistryKey)
	t.Setenv(registryVersionEnv, "1")
	t.Setenv(registryServiceEnv, testRegistryService)
	t.Setenv(registryAccessEnv, "90s")
	t.Setenv(registryPullEnv, "2m")
	t.Setenv(registrySweepEnv, "30s")
	got := newAPIConfig().RegistryAuth
	if got.AccessTokenLifetime != 90*time.Second || got.PullCredentialLifetime != 2*time.Minute {
		t.Fatalf("lifetimes = %s and %s, want 1m30s and 2m", got.AccessTokenLifetime, got.PullCredentialLifetime)
	}
	if got.CredentialSweep != 30*time.Second {
		t.Fatalf("credential sweep interval = %s, want 30s", got.CredentialSweep)
	}
}

func TestRegistryAuthPanicsOnInvalidConfiguration(t *testing.T) {
	token := map[string]string{
		registryAuthEnv:    locoControllerV1.RegistryAuthToken,
		registryHostEnv:    testRegistryHost,
		registryKeyEnv:     testRegistryKey,
		registryVersionEnv: "1",
		registryServiceEnv: testRegistryService,
	}
	tests := []struct {
		name string
		env  map[string]string
		want error
	}{
		{"unknown mode", map[string]string{registryAuthEnv: "bearer"}, errInvalidRegistryAuth},
		{"mode in capitals", map[string]string{registryAuthEnv: "Token"}, errInvalidRegistryAuth},
		{"token without a key", map[string]string{registryKeyEnv: ""}, transit.ErrKeyName},
		{
			"token with a key path",
			map[string]string{registryKeyEnv: "transit/" + testRegistryKey},
			errInvalidRegistryKey,
		},
		{"token with the ingest key", map[string]string{registryKeyEnv: testIngestKey}, errRegistryKeyShared},
		{"token with the secrets key", map[string]string{registryKeyEnv: testTransitKey}, errRegistryKeyShared},
		{"token without a version", map[string]string{registryVersionEnv: ""}, errInvalidRegistryVersion},
		{"token with version zero", map[string]string{registryVersionEnv: "0"}, errInvalidRegistryVersion},
		{"token with a negative version", map[string]string{registryVersionEnv: "-1"}, errInvalidRegistryVersion},
		{"token with a version name", map[string]string{registryVersionEnv: "v1"}, envconfig.ErrInvalidInt32},
		{"token without a service", map[string]string{registryServiceEnv: ""}, errMissingRegistryService},
		{"token without a registry host", map[string]string{registryHostEnv: ""}, errRegistryTokenNoHost},
		{
			"token with a registry url path",
			map[string]string{registryURLEnv: "https://" + testRegistryHost + "/v2"},
			registryclient.ErrURLInvalid,
		},
		{
			"token with an ftp registry url",
			map[string]string{registryURLEnv: "ftp://" + testRegistryHost},
			errInvalidRegistry,
		},
		{
			"token with a zero registry timeout",
			map[string]string{"LOCO_REGISTRY_TIMEOUT": "0s"},
			envconfig.ErrNotPositive,
		},
		{"token with a zero access lifetime", map[string]string{registryAccessEnv: "0s"}, envconfig.ErrNotPositive},
		{"token with a negative pull lifetime", map[string]string{registryPullEnv: "-1h"}, envconfig.ErrNotPositive},
		{"token with a pull lifetime in hours", map[string]string{registryPullEnv: "24"}, envconfig.ErrInvalidDuration},
		{"token with a zero sweep interval", map[string]string{registrySweepEnv: "0s"}, envconfig.ErrNotPositive},
		{
			"token with static credentials",
			map[string]string{
				registryHostEnv: testRegistryHost,
				registryUserEnv: testRegistryUser,
				registryPassEnv: testRegistryPass,
			},
			errRegistryStaticOnly,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearAPIConfigEnv(t)
			for key, value := range token {
				t.Setenv(key, value)
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			err := panicValue(t, func() { newAPIConfig() })
			if !errors.Is(err, tt.want) {
				t.Errorf("panic = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestImageResolveTimeoutConfiguration(t *testing.T) {
	clearAPIConfigEnv(t)
	if got := newAPIConfig().ImageResolveTimeout; got != 15*time.Second {
		t.Fatalf("default image resolve timeout = %s", got)
	}
	t.Setenv("LOCO_IMAGE_RESOLVE_TIMEOUT", "25s")
	if got := newAPIConfig().ImageResolveTimeout; got != 25*time.Second {
		t.Fatalf("configured image resolve timeout = %s", got)
	}
	t.Setenv("LOCO_IMAGE_RESOLVE_TIMEOUT", "0s")
	if err := panicValue(t, func() { newAPIConfig() }); !errors.Is(err, envconfig.ErrNotPositive) {
		t.Fatalf("panic = %v, want positive duration validation", err)
	}
}

func TestNewAPIConfigTokenMaximum(t *testing.T) {
	clearAPIConfigEnv(t)
	if got := newAPIConfig().Tokens.MaxAPITokenDuration; got != defaultAPITokenMaxLifetime {
		t.Fatalf("maximum = %s", got)
	}
	t.Setenv("LOCO_API_TOKEN_MAX_LIFETIME", "42h")
	if got := newAPIConfig().Tokens.MaxAPITokenDuration; got != 42*time.Hour {
		t.Fatalf("maximum = %s, want 42h", got)
	}
	t.Setenv("LOCO_API_TOKEN_MAX_LIFETIME", "500ms")
	defer func() {
		if got, ok := recover().(error); !ok || !errors.Is(got, errAPITokenLifetime) {
			t.Fatalf("panic = %v", got)
		}
	}()
	newAPIConfig()
}

func TestServiceDefaultsAdvertiseInstallationLogRetention(t *testing.T) {
	t.Setenv("CLICKHOUSE_LOGS_TTL", "72h")
	defaults := newServiceDefaults()
	if defaults.LogRetention != 72*time.Hour {
		t.Fatalf("configured retention = %v", defaults.LogRetention)
	}
	if err := defaults.ValidateLogRetention("4d"); err == nil {
		t.Fatal("accepted retention above installation maximum")
	}
	if err := defaults.ValidateLogRetention("2d"); err != nil {
		t.Fatal(err)
	}
}
