package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/pkg/registryclient"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	"github.com/team-loco/loco/api/service"
)

const (
	configuredOrigin = "https://app.loco.build"
	maxReplicasEnv   = "LOCO_DEFAULT_MAX_REPLICAS"
	registryHostEnv  = "LOCO_REGISTRY_HOST"
	registryUserEnv  = "LOCO_REGISTRY_USERNAME"
	registryPassEnv  = "LOCO_REGISTRY_PASSWORD"
	testRegistryHost = "registry.loco.test"
	testRegistryUser = "api"
	testRegistryPass = "secret"
)

func preflightAllowed(t *testing.T, h http.Handler, origin string) bool {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/loco.user.v1.UserService/WhoAmI", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Origin") == origin
}

func TestWithCORS(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	tests := []struct {
		name          string
		allowLoopback bool
		origin        string
		want          bool
	}{
		{"configured origin outside production", true, configuredOrigin, true},
		{"configured origin in production", false, configuredOrigin, true},
		{"localhost on any port outside production", true, "http://localhost:5199", true},
		{"ipv4 loopback outside production", true, "http://127.0.0.1:3000", true},
		{"ipv6 loopback outside production", true, "http://[::1]:5173", true},
		{"https localhost outside production", true, "https://localhost", true},
		{"localhost in production", false, "http://localhost:5199", false},
		{"other host outside production", true, "https://evil.example", false},
		{"localhost-prefixed host outside production", true, "http://localhost.evil.example", false},
		{"non-http scheme outside production", true, "file://localhost", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := withCORS([]string{configuredOrigin}, tt.allowLoopback)
			h := middleware(next)
			if got := preflightAllowed(t, h, tt.origin); got != tt.want {
				t.Errorf("origin %q allowed = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func panicValue(t *testing.T, fn func()) error {
	t.Helper()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fn()
	}()
	if recovered == nil {
		t.Fatal("expected a panic")
	}
	err, ok := recovered.(error)
	if !ok {
		t.Fatalf("panic value %v is not an error", recovered)
	}
	return err
}

func clearAPIConfigEnv(t *testing.T) {
	t.Helper()
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
	want := servicedefaults.Defaults{
		CPU:         defaultServiceCPU,
		Memory:      defaultServiceMemory,
		MinReplicas: defaultServiceMinReplicas,
		MaxReplicas: defaultServiceMaxReplicas,
		PathPrefix:  defaultServicePathPrefix,
		IdleTimeout: defaultServiceIdleTimeout,
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

func TestNewAPIConfigReadsServiceDefaults(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_DEFAULT_CPU", "250m")
	t.Setenv("LOCO_DEFAULT_MEMORY", "512Mi")
	t.Setenv("LOCO_DEFAULT_MIN_REPLICAS", "2")
	t.Setenv(maxReplicasEnv, "4")
	t.Setenv("LOCO_DEFAULT_PATH_PREFIX", "/app")
	t.Setenv("LOCO_DEFAULT_IDLE_TIMEOUT", "120")
	ac := newAPIConfig()
	want := servicedefaults.Defaults{
		CPU:         "250m",
		Memory:      "512Mi",
		MinReplicas: 2,
		MaxReplicas: 4,
		PathPrefix:  "/app",
		IdleTimeout: 120,
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
		{"zero image retention", map[string]string{"LOCO_IMAGE_RETENTION": "0"}, errNotPositive},
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

func TestNewAPIConfigPanicsOnPartialSourceBucket(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_SOURCE_BUCKET", "loco-sources")
	err := panicValue(t, func() { newAPIConfig() })
	if !strings.Contains(err.Error(), "source bucket") {
		t.Errorf("panic = %v, want a source bucket error", err)
	}
}
