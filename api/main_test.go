package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/team-loco/loco/api/pkg/servicedefaults"
)

const (
	configuredOrigin = "https://app.loco.build"
	maxReplicasEnv   = "LOCO_DEFAULT_MAX_REPLICAS"
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
