package migrations

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

const (
	unreachableDSN    = "clickhouse://127.0.0.1:1?dial_timeout=100ms"
	testRetryBudget   = 300 * time.Millisecond
	testRetryInterval = 50 * time.Millisecond
	retrySlack        = 2 * time.Second
	testTTL           = 24 * time.Hour
)

func validConfig() Config {
	return Config{
		DSN:        unreachableDSN,
		Database:   "loco_obs",
		LogsTTL:    testTTL,
		TracesTTL:  testTTL,
		MetricsTTL: testTTL,
	}
}

func TestIntervalUsesLargestWholeUnit(t *testing.T) {
	cases := map[time.Duration]string{
		72 * time.Hour:   "toIntervalDay(3)",
		36 * time.Hour:   "toIntervalHour(36)",
		90 * time.Minute: "toIntervalMinute(90)",
		61 * time.Second: "toIntervalSecond(61)",
	}
	for ttl, want := range cases {
		if got := interval(ttl); got != want {
			t.Errorf("interval(%s) = %q, want %q", ttl, got, want)
		}
	}
}

func TestValidateRejectsInvalidConfig(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Config)
		want   error
	}{
		"database with a dash":      {func(c *Config) { c.Database = "loco-obs" }, ErrInvalidDatabase},
		"database with a statement": {func(c *Config) { c.Database = "x; DROP TABLE y" }, ErrInvalidDatabase},
		"empty database":            {func(c *Config) { c.Database = "" }, ErrInvalidDatabase},
		"zero logs ttl":             {func(c *Config) { c.LogsTTL = 0 }, ErrInvalidTTL},
		"negative traces ttl":       {func(c *Config) { c.TracesTTL = -time.Hour }, ErrInvalidTTL},
		"fractional metrics ttl":    {func(c *Config) { c.MetricsTTL = 1500 * time.Millisecond }, ErrInvalidTTL},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if err := Up(t.Context(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("Up error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRenderedMigrationsHaveNoPlaceholdersLeft(t *testing.T) {
	cfg := validConfig()
	cfg.Database = "tenant_obs"
	rendered, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	names, err := fs.Glob(rendered, "*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no migrations rendered")
	}
	for _, name := range names {
		content, err := fs.ReadFile(rendered, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(content)
		if strings.Contains(text, "${") {
			t.Errorf("%s still contains a placeholder", name)
		}
		if !strings.Contains(text, "tenant_obs.") {
			t.Errorf("%s does not use the configured database", name)
		}
		if !strings.Contains(text, "-- +goose NO TRANSACTION") {
			t.Errorf("%s runs inside a transaction, which ClickHouse does not support", name)
		}
	}
}

func TestSubstituteRejectsUnknownPlaceholder(t *testing.T) {
	_, err := substitute([]byte("CREATE TABLE ${DATABASE}.t TTL ${UNKNOWN}"), map[string]string{"DATABASE": "db"})
	if !errors.Is(err, ErrUnknownPlaceholder) {
		t.Fatalf("substitute error = %v, want %v", err, ErrUnknownPlaceholder)
	}
}

func TestUpWithRetryGivesUpAfterBudget(t *testing.T) {
	started := time.Now()
	err := UpWithRetry(t.Context(), validConfig(), Retry{Budget: testRetryBudget, Interval: testRetryInterval})
	elapsed := time.Since(started)
	if !errors.Is(err, ErrRetryBudgetExhausted) {
		t.Fatalf("UpWithRetry error = %v, want %v", err, ErrRetryBudgetExhausted)
	}
	if elapsed < testRetryBudget {
		t.Errorf("UpWithRetry returned after %s, before its %s budget", elapsed, testRetryBudget)
	}
	if elapsed > testRetryBudget+retrySlack {
		t.Errorf("UpWithRetry returned after %s, long after its %s budget", elapsed, testRetryBudget)
	}
}

func TestUpWithRetryStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := UpWithRetry(ctx, validConfig(), Retry{Budget: time.Hour, Interval: testRetryInterval})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UpWithRetry error = %v, want %v", err, context.Canceled)
	}
}

func TestUpWithRetryRejectsInvalidConfigWithoutRetrying(t *testing.T) {
	cfg := validConfig()
	cfg.Database = "loco-obs"
	started := time.Now()
	err := UpWithRetry(t.Context(), cfg, Retry{Budget: time.Hour, Interval: testRetryInterval})
	if !errors.Is(err, ErrInvalidDatabase) {
		t.Fatalf("UpWithRetry error = %v, want %v", err, ErrInvalidDatabase)
	}
	if elapsed := time.Since(started); elapsed > retrySlack {
		t.Errorf("UpWithRetry retried an invalid configuration for %s", elapsed)
	}
}
