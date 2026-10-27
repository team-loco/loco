package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/service"
	"github.com/team-loco/loco/internal/bearer"
)

const (
	maxReplicasEnv     = "LOCO_DEFAULT_MAX_REPLICAS"
	registryHostEnv    = "LOCO_REGISTRY_HOST"
	registryUserEnv    = "LOCO_REGISTRY_USERNAME"
	registryPassEnv    = "LOCO_REGISTRY_PASSWORD"
	testRegistryHost   = "registry.loco.test"
	testRegistryUser   = "api"
	testRegistryPass   = "secret"
	retentionDaysEnv   = "EVENTS_RETENTION_DAYS"
	installHooksEnv    = "INSTALL_WEBHOOKS"
	orgLinksEnv        = "ORG_LINKS"
	authIssuersEnv     = "AUTH_ISSUERS"
	testAdminEnv       = "LOCO_TEST_ADMIN_TOKEN"
	testAdminToken     = "service-key"
	testIssuer         = "https://issuer.loco.test"
	testHookURL        = "https://hooks.loco.test/events"
	testDatabaseURL    = "postgres://db.loco.test:5432/loco"
	testHookSecret     = "whsec_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	transitAddrEnv     = "LOCO_SECRETS_TRANSIT_ADDR"
	transitMountEnv    = "LOCO_SECRETS_TRANSIT_MOUNT"
	transitKeyEnv      = "LOCO_SECRETS_TRANSIT_KEY"
	transitMethodEnv   = "LOCO_SECRETS_TRANSIT_AUTH"
	transitAuthEnv     = "LOCO_SECRETS_TRANSIT_TOKEN"
	transitKubeMount   = "LOCO_SECRETS_TRANSIT_KUBERNETES_MOUNT"
	transitKubeRole    = "LOCO_SECRETS_TRANSIT_KUBERNETES_ROLE"
	transitKubeJWTFile = "LOCO_SECRETS_TRANSIT_KUBERNETES_TOKEN_FILE"
	transitCAEnv       = "LOCO_SECRETS_TRANSIT_CA_FILE"
	transitTimeEnv     = "LOCO_SECRETS_TRANSIT_TIMEOUT"
	transitMarginEnv   = "LOCO_SECRETS_TRANSIT_RENEW_MARGIN"
	transitRetryEnv    = "LOCO_SECRETS_TRANSIT_RENEW_RETRY"
	transitCacheEnv    = "LOCO_SECRETS_TRANSIT_CACHE_TTL"
	testTransitAddr    = "https://bao.loco.test:8200"
	testTransitKey     = "loco-dek"
	testTransitAuth    = "s.transit"
	testTransitRole    = "loco-api"
	testTransitKube    = "kubernetes"
	testTransitJWT     = "/var/run/secrets/openbao/token"
)

var adminIssuers = `[{"issuer":"` + testIssuer + `","audience":"loco",` +
	`"admin":{"type":"supabase","tokenEnv":"` + testAdminEnv + `"}}]`

func clearAPIConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LOCO_PLACEMENT_RETRY_DELAY", "")
	t.Setenv("LOCO_PLACEMENT_MAX_APPLY_ATTEMPTS", "")
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
	t.Setenv(registryHostEnv, "")
	t.Setenv("LOCO_REGISTRY_PREFIX", "")
	t.Setenv("LOCO_REGISTRY_URL", "")
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
	t.Setenv(retentionDaysEnv, "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv(installHooksEnv, "")
	t.Setenv(orgLinksEnv, "")
	t.Setenv(authIssuersEnv, "")
	t.Setenv("AUTH_SIGNUP_MODE", "")
	t.Setenv("AUTH_SIGNUP_DOMAINS", "")
	t.Setenv("MIN_CLI_VERSION", "")
	t.Setenv(testAdminEnv, "")
	t.Setenv(transitAddrEnv, testTransitAddr)
	t.Setenv(transitMountEnv, "")
	t.Setenv(transitKeyEnv, testTransitKey)
	t.Setenv(transitMethodEnv, secretkeys.TransitAuthToken)
	t.Setenv(transitAuthEnv, testTransitAuth)
	t.Setenv(transitKubeMount, "")
	t.Setenv(transitKubeRole, "")
	t.Setenv(transitKubeJWTFile, "")
	t.Setenv(transitCAEnv, "")
	t.Setenv(transitTimeEnv, "")
	t.Setenv(transitMarginEnv, "")
	t.Setenv(transitRetryEnv, "")
	t.Setenv(transitCacheEnv, "")
	t.Setenv("LOCO_SECRETS_MAX_VALUE_BYTES", "")
	t.Setenv("LOCO_SECRETS_MAX_PER_SERVICE", "")
	t.Setenv("LOCO_SECRETS_MAX_PLACEMENT_BYTES", "")
	t.Setenv("LOCO_SECRETS_LOCK_TIMEOUT", "")
	t.Setenv(bootstrapEnv, "")
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
		PathPrefix:               defaultServicePathPrefix,
		IdleTimeout:              defaultServiceIdleTimeout,
		Port:                     defaultServicePort,
		HealthPath:               defaultHealthPath,
		HealthInterval:           defaultHealthInterval,
		HealthTimeout:            defaultHealthTimeout,
		HealthFailThreshold:      defaultHealthFailThresh,
		HealthStartupGracePeriod: defaultHealthGracePeriod,
	}
	if ac.ServiceDefaults != want {
		t.Errorf("service defaults = %+v, want %+v", ac.ServiceDefaults, want)
	}
	wantSweep := service.ImageSweepConfig{
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
	wantSource := service.SourceSweepConfig{
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
	if ac.EventsRetention != defaultEventsRetentionDays*day {
		t.Errorf("events retention = %v, want %d days", ac.EventsRetention, defaultEventsRetentionDays)
	}
	if len(ac.InstallWebhooks) != 0 || len(ac.AuthIssuers) != 0 {
		t.Errorf("install webhooks = %v, issuers = %v, want none", ac.InstallWebhooks, ac.AuthIssuers)
	}
	if ac.SignupPolicy.Mode != auth.SignupOpen {
		t.Errorf("signup mode = %q, want %q", ac.SignupPolicy.Mode, auth.SignupOpen)
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
			if !errors.Is(err, errNotPositive) {
				t.Errorf("panic = %v, want %v", err, errNotPositive)
			}
		})
	}
}

func TestNewAPIConfigReadsEventsRetention(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(retentionDaysEnv, "7")
	ac := newAPIConfig()
	if ac.EventsRetention != 7*day {
		t.Errorf("events retention = %v, want 7 days", ac.EventsRetention)
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
	t.Setenv("AUTH_SIGNUP_MODE", "domains")
	t.Setenv("AUTH_SIGNUP_DOMAINS", "acme.test")
	t.Setenv("MIN_CLI_VERSION", "v0.0.61")
	ac := newAPIConfig()
	if len(ac.AuthIssuers) != 1 || ac.AuthIssuers[0].Admin == nil {
		t.Fatalf("issuers = %+v, want one with an admin", ac.AuthIssuers)
	}
	if got := ac.AuthIssuers[0].Admin.Token; got != testAdminToken {
		t.Errorf("admin token = %q, want %q", got, testAdminToken)
	}
	wantDomains := []string{"acme.test"}
	if ac.SignupPolicy.Mode != auth.SignupDomains || !slices.Equal(ac.SignupPolicy.Domains, wantDomains) {
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
	if ac.Registry != wantRegistry {
		t.Errorf("registry = %+v, want %+v", ac.Registry, wantRegistry)
	}
	wantSweep := service.ImageSweepConfig{
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

	t.Setenv("LOCO_REGISTRY_URL", "http://localhost:5001")
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

func TestNewAPIConfigReadsServiceDefaults(t *testing.T) {
	clearAPIConfigEnv(t)
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
	ac := newAPIConfig()
	want := servicedefaults.Defaults{
		CPU:                      "250m",
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
	}
	if ac.ServiceDefaults != want {
		t.Errorf("service defaults = %+v, want %+v", ac.ServiceDefaults, want)
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
			errInvalidForcePathStyle,
		},
		{
			"non-boolean webhook private networks",
			map[string]string{"WEBHOOK_ALLOW_PRIVATE_NETWORKS": "sometimes"},
			errInvalidWebhooksPrivate,
		},
		{"invalid default cpu", map[string]string{"LOCO_DEFAULT_CPU": "lots"}, errInvalidServiceDefault},
		{"invalid default memory", map[string]string{"LOCO_DEFAULT_MEMORY": "-1Gi"}, errInvalidServiceDefault},
		{"zero default replicas", map[string]string{"LOCO_DEFAULT_MIN_REPLICAS": "0"}, errInvalidServiceDefault},
		{"non-numeric default replicas", map[string]string{maxReplicasEnv: "many"}, errInvalidInt32},
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
		{"non-numeric image retention", map[string]string{"LOCO_IMAGE_RETENTION": "all"}, errInvalidInt32},
		{"non-duration sweep interval", map[string]string{"LOCO_IMAGE_SWEEP_INTERVAL": "10"}, errInvalidDuration},
		{"negative sweep interval", map[string]string{"LOCO_IMAGE_SWEEP_INTERVAL": "-1m"}, errNotPositive},
		{"zero build batch", map[string]string{"LOCO_IMAGE_SWEEP_BUILD_BATCH": "0"}, errNotPositive},
		{"zero repository batch", map[string]string{"LOCO_IMAGE_SWEEP_REPOSITORY_BATCH": "0"}, errNotPositive},
		{"zero tag batch", map[string]string{"LOCO_IMAGE_SWEEP_TAG_BATCH": "0"}, errNotPositive},
		{"zero tag min age", map[string]string{"LOCO_IMAGE_SWEEP_TAG_MIN_AGE": "0s"}, errNotPositive},
		{
			"non-duration source sweep interval",
			map[string]string{"LOCO_SOURCE_SWEEP_INTERVAL": "5"},
			errInvalidDuration,
		},
		{"zero upload grace", map[string]string{"LOCO_SOURCE_SWEEP_UPLOAD_GRACE": "0s"}, errNotPositive},
		{"negative orphan min age", map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_MIN_AGE": "-1h"}, errNotPositive},
		{"zero source build batch", map[string]string{"LOCO_SOURCE_SWEEP_BUILD_BATCH": "0"}, errNotPositive},
		{"zero orphan page size", map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_PAGE_SIZE": "0"}, errNotPositive},
		{"non-numeric orphan max pages", map[string]string{"LOCO_SOURCE_SWEEP_ORPHAN_MAX_PAGES": "x"}, errInvalidInt32},
		{"zero registry timeout", map[string]string{"LOCO_REGISTRY_TIMEOUT": "0s"}, errNotPositive},
		{"zero events retention", map[string]string{retentionDaysEnv: "0"}, errNotPositive},
		{"non-numeric events retention", map[string]string{retentionDaysEnv: "forever"}, errInvalidInt32},
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
		{"unknown signup mode", map[string]string{"AUTH_SIGNUP_MODE": "invite"}, errInvalidSignupPolicy},
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
				registryHostEnv:     testRegistryHost,
				registryUserEnv:     testRegistryUser,
				registryPassEnv:     testRegistryPass,
				"LOCO_REGISTRY_URL": "https://registry.loco.test/v2",
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
	got := newAPIConfig().Secrets
	want := secretkeys.TransitConfig{
		Address:     testTransitAddr,
		Mount:       defaultSecretsTransitMount,
		Key:         testTransitKey,
		Auth:        secretkeys.TransitAuthToken,
		Token:       testTransitAuth,
		Timeout:     defaultSecretsTransitTimeout,
		RenewMargin: defaultSecretsTransitMargin,
		RenewRetry:  defaultSecretsTransitRetry,
		CacheTTL:    defaultSecretsTransitDEKTTL,
	}
	if got != want {
		t.Errorf("transit config = %+v, want %+v", got, want)
	}

	t.Setenv(transitMountEnv, "kms/transit")
	t.Setenv(transitCAEnv, "/etc/loco/bao-ca.pem")
	t.Setenv(transitTimeEnv, "3s")
	t.Setenv(transitMarginEnv, "1m")
	t.Setenv(transitRetryEnv, "2s")
	t.Setenv(transitCacheEnv, "30s")
	got = newAPIConfig().Secrets
	want.Mount = "kms/transit"
	want.CAFile = "/etc/loco/bao-ca.pem"
	want.Timeout = 3 * time.Second
	want.RenewMargin = time.Minute
	want.RenewRetry = 2 * time.Second
	want.CacheTTL = 30 * time.Second
	if got != want {
		t.Errorf("transit config = %+v, want %+v", got, want)
	}
}

func TestNewAPIConfigReadsTransitKubernetesAuth(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(transitMethodEnv, secretkeys.TransitAuthKubernetes)
	t.Setenv(transitAuthEnv, "")
	t.Setenv(transitKubeMount, testTransitKube)
	t.Setenv(transitKubeRole, testTransitRole)
	t.Setenv(transitKubeJWTFile, testTransitJWT)
	got := newAPIConfig().Secrets
	want := secretkeys.TransitKubernetesConfig{Mount: testTransitKube, Role: testTransitRole, TokenFile: testTransitJWT}
	if got.Auth != secretkeys.TransitAuthKubernetes || got.Token != "" || got.Kubernetes != want {
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
			secretkeys.ErrTransitAddress,
		},
		{
			"transit without a token",
			map[string]string{
				transitAuthEnv: "",
			},
			secretkeys.ErrTransitToken,
		},
		{
			"transit without an auth method",
			map[string]string{
				transitMethodEnv: "",
			},
			secretkeys.ErrTransitAuth,
		},
		{
			"kubernetes auth without a role",
			map[string]string{
				transitMethodEnv:   secretkeys.TransitAuthKubernetes,
				transitAuthEnv:     "",
				transitKubeMount:   testTransitKube,
				transitKubeJWTFile: testTransitJWT,
			},
			secretkeys.ErrTransitKubernetes,
		},
		{
			"kubernetes auth with a token",
			map[string]string{
				transitMethodEnv:   secretkeys.TransitAuthKubernetes,
				transitKubeMount:   testTransitKube,
				transitKubeRole:    testTransitRole,
				transitKubeJWTFile: testTransitJWT,
			},
			secretkeys.ErrTransitKubernetes,
		},
		{
			"transit without a key",
			map[string]string{
				transitKeyEnv: "",
			},
			secretkeys.ErrTransitKeyName,
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

func TestPlacementRetryDelayConfiguration(t *testing.T) {
	t.Setenv("LOCO_PLACEMENT_RETRY_DELAY", "")
	if got := newAgentConfig().PlacementRetryDelay; got != defaultPlacementRetryDelay {
		t.Fatalf("retry delay = %v", got)
	}
	t.Setenv("LOCO_PLACEMENT_RETRY_DELAY", "30m")
	if got := newAgentConfig().PlacementRetryDelay; got != 30*time.Minute {
		t.Fatalf("retry delay = %v, want 30m", got)
	}
	for _, value := range []string{"0s", "-1m", "500ms", "bad"} {
		t.Setenv("LOCO_PLACEMENT_RETRY_DELAY", value)
		if err := panicValue(t, func() { newAgentConfig() }); err == nil {
			t.Fatalf("invalid retry delay %s accepted", value)
		}
	}
}

func TestPlacementMaxApplyAttemptsConfiguration(t *testing.T) {
	t.Setenv("LOCO_PLACEMENT_MAX_APPLY_ATTEMPTS", "")
	if got := newAgentConfig().PlacementMaxApplyAttempts; got != defaultPlacementMaxApplyAttempts {
		t.Fatalf("max apply attempts = %d", got)
	}
	t.Setenv("LOCO_PLACEMENT_MAX_APPLY_ATTEMPTS", "5")
	if got := newAgentConfig().PlacementMaxApplyAttempts; got != 5 {
		t.Fatalf("max apply attempts = %d, want 5", got)
	}
	for _, value := range []string{"0", "-1", "bad"} {
		t.Setenv("LOCO_PLACEMENT_MAX_APPLY_ATTEMPTS", value)
		if err := panicValue(t, func() { newAgentConfig() }); err == nil {
			t.Fatalf("invalid max apply attempts %s accepted", value)
		}
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
		if got := tc.read(newAgentConfig()); got != tc.fallback {
			t.Fatalf("%s default = %v, want %v", tc.env, got, tc.fallback)
		}
		t.Setenv(tc.env, "10m")
		if got := tc.read(newAgentConfig()); got != 10*time.Minute {
			t.Fatalf("%s = %v, want 10m", tc.env, got)
		}
		t.Setenv(tc.env, "0s")
		if err := panicValue(t, func() { newAgentConfig() }); err == nil {
			t.Fatalf("%s accepted 0s", tc.env)
		}
		t.Setenv(tc.env, "")
	}
}
