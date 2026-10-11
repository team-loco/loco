package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"

	"github.com/team-loco/loco/observability-proxy/internal/testenv"
	"github.com/team-loco/loco/observability-proxy/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	startupDeadline         = 2 * time.Second
	healthzDeadline         = 2 * time.Second
	testTokenTTL            = time.Second
	testMaxConcurrent       = 1
	testRetryBudget         = 300 * time.Millisecond
	testRetryInterval       = 50 * time.Millisecond
	testTTL                 = time.Hour
	unreachableControlPlane = "http://127.0.0.1:1"
	unreachableMigrator     = "clickhouse://127.0.0.1:1"
	proxyDatabase           = "loco_obs"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

func silentListener(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)
	wg.Go(func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close listener: %v", err)
		}
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Errorf("close conn: %v", err)
			}
		}
	})
	return ln
}

func TestHealthzServedWhenClickHouseNeverAnswers(t *testing.T) {
	ln := silentListener(t)
	addr := ln.Addr().String()
	cfg := &config.Config{
		ControlPlaneURL: unreachableControlPlane,
		ClickHouseURL:   "clickhouse://" + addr,
		ClickHouseDB:    "default",
		MaxConcurrent:   testMaxConcurrent,
		TokenCacheTTL:   testTokenTTL,
	}

	started := make(chan *proxy, 1)
	startErr := make(chan error, 1)
	go func() {
		p, err := newProxy(cfg, migrationlock.None{})
		if err != nil {
			startErr <- err
			return
		}
		started <- p
	}()

	var p *proxy
	select {
	case p = <-started:
	case err := <-startErr:
		t.Fatalf("newProxy: %v", err)
	case <-time.After(startupDeadline):
		t.Fatalf("proxy did not start within %s while clickhouse never answered", startupDeadline)
	}
	t.Cleanup(p.close)

	srv := httptest.NewServer(p.server.Handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	client := &http.Client{Timeout: healthzDeadline}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestStartupDiagnostics(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	cfg := &config.Config{
		Port:            8080,
		ControlPlaneURL: "https://api.example.test?token=control-plane-password",
		ClickHouseURL:   "clickhouse://reader:database-password@db.example.test:9000?password=query-password",
		ClickHouseDB:    "telemetry",
		ProxyAuthToken:  "proxy-password",
	}
	logStartup(logger, cfg, "test-version")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode startup diagnostics: %v", err)
	}
	for key, want := range map[string]string{"version": "test-version", "clickhouse_database": "telemetry"} {
		if record[key] != want {
			t.Errorf("%s = %v, want %s", key, record[key], want)
		}
	}
	if record["port"] != float64(cfg.Port) {
		t.Errorf("port = %v, want %d", record["port"], cfg.Port)
	}
	credentials := []string{
		"database-password", "query-password", "control-plane-password", "proxy-password",
	}
	for _, credential := range credentials {
		if strings.Contains(output.String(), credential) {
			t.Errorf("startup diagnostics contain credential %q", credential)
		}
	}
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Timeout: healthzDeadline, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	return resp.StatusCode
}

func TestReadyzUnavailableUntilMigrationsSucceed(t *testing.T) {
	ln := silentListener(t)
	addr := ln.Addr().String()
	cfg := &config.Config{
		ControlPlaneURL:        unreachableControlPlane,
		ClickHouseURL:          "clickhouse://" + addr,
		ClickHouseMigratorURL:  unreachableMigrator,
		ClickHouseDB:           proxyDatabase,
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   testRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg, migrationlock.None{})
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)

	srv := httptest.NewServer(p.server.Handler)
	t.Cleanup(srv.Close)

	if status := getStatus(t, srv.URL+"/readyz"); status != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz before migrations status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if err := p.migrate(t.Context(), cfg); err == nil {
		t.Fatal("migrate succeeded against an unreachable ClickHouse")
	}
	if status := getStatus(t, srv.URL+"/readyz"); status != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz after failed migrations status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if status := getStatus(t, srv.URL+"/healthz"); status != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", status, http.StatusOK)
	}
}

func TestServesProbesWhileMigrating(t *testing.T) {
	ln := silentListener(t)
	cfg := &config.Config{
		ControlPlaneURL:        unreachableControlPlane,
		ClickHouseURL:          "clickhouse://" + ln.Addr().String(),
		ClickHouseMigratorURL:  unreachableMigrator,
		ClickHouseDB:           proxyDatabase,
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   testRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg, migrationlock.None{})
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)

	var lc net.ListenConfig
	serverLn, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := "http://" + serverLn.Addr().String()

	runErr := make(chan error, 1)
	go func() { runErr <- p.run(t.Context(), cfg, serverLn) }()

	if status := getStatus(t, base+"/healthz"); status != http.StatusOK {
		t.Fatalf("GET /healthz while migrating status = %d, want %d", status, http.StatusOK)
	}
	if status := getStatus(t, base+"/readyz"); status != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz while migrating status = %d, want %d", status, http.StatusServiceUnavailable)
	}

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("run succeeded against an unreachable ClickHouse")
		}
	case <-time.After(startupDeadline):
		t.Fatalf("run did not return within %s after the retry budget ran out", startupDeadline)
	}
}

type heldElsewhere struct{}

func (heldElsewhere) WithLock(ctx context.Context, _ func(context.Context) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRetryBudgetCoversWaitingForTheMigrationLock(t *testing.T) {
	ln := silentListener(t)
	cfg := &config.Config{
		ControlPlaneURL:        unreachableControlPlane,
		ClickHouseURL:          "clickhouse://" + ln.Addr().String(),
		ClickHouseMigratorURL:  unreachableMigrator,
		ClickHouseDB:           proxyDatabase,
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   testRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg, heldElsewhere{})
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)

	migrateErr := make(chan error, 1)
	go func() { migrateErr <- p.migrate(t.Context(), cfg) }()
	select {
	case err := <-migrateErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("migrate() = %v, want %v", err, context.DeadlineExceeded)
		}
	case <-time.After(startupDeadline):
		t.Fatalf("migrate waited for the lock past the %s retry budget", testRetryBudget)
	}
	if p.migrated.Load() {
		t.Fatal("proxy marked migrated without holding the lock")
	}
}

func TestNoneMigrationLockRunsWithoutKubernetes(t *testing.T) {
	lock, err := newMigrationLock(&config.Config{MigrationLock: config.MigrationLockNone})
	if err != nil {
		t.Fatalf("newMigrationLock(none): %v", err)
	}
	if _, ok := lock.(migrationlock.None); !ok {
		t.Fatalf("newMigrationLock(none) = %T, want migrationlock.None", lock)
	}
}

func TestKubernetesMigrationLockNeedsTheInClusterConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	_, err := newMigrationLock(&config.Config{MigrationLock: config.MigrationLockKubernetes})
	if !errors.Is(err, rest.ErrNotInCluster) {
		t.Fatalf("newMigrationLock(kubernetes) outside a cluster = %v, want %v", err, rest.ErrNotInCluster)
	}
}

const (
	testClickHouseEnv   = "LOCO_TEST_CLICKHOUSE_URL"
	databaseNameBytes   = 4
	testLeaseDuration   = 2 * time.Second
	testRenewDeadline   = time.Second
	testRetryPeriod     = 200 * time.Millisecond
	sharedRetryBudget   = time.Minute
	readinessDeadline   = time.Minute
	readinessPollPeriod = 50 * time.Millisecond
)

func testClickHouse(t *testing.T) (driver.Conn, string, string) {
	t.Helper()
	dsn := os.Getenv(testClickHouseEnv)
	if dsn == "" {
		t.Skip(testClickHouseEnv + " not set")
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", testClickHouseEnv, err)
	}
	admin, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	suffix := make([]byte, databaseNameBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	database := "loco_obs_proxy_test_" + hex.EncodeToString(suffix)
	t.Cleanup(func() {
		if err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+database+" SYNC"); err != nil {
			t.Errorf("drop %s: %v", database, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close clickhouse: %v", err)
		}
	})
	return admin, dsn, database
}

func waitReady(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(readinessDeadline)
	for time.Now().Before(deadline) {
		if getStatus(t, base+"/readyz") == http.StatusOK {
			return
		}
		time.Sleep(readinessPollPeriod)
	}
	t.Fatalf("%s did not become ready within %s", base, readinessDeadline)
}

func TestReplicasTakeTurnsMigratingUnderTheLease(t *testing.T) {
	server := testenv.APIServer(t)
	admin, dsn, database := testClickHouse(t)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(t.Output(), nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client, err := coordinationv1client.NewForConfig(server)
	if err != nil {
		t.Fatalf("coordination client: %v", err)
	}
	leaseName := strings.ReplaceAll(database, "_", "-")
	ctx, stop := context.WithCancel(t.Context())
	identities := []string{"obs-proxy-a", "obs-proxy-b"}
	bases := make([]string, 0, len(identities))
	runErrs := make(chan error, len(identities))
	for _, identity := range identities {
		cfg := &config.Config{
			ControlPlaneURL:        unreachableControlPlane,
			ClickHouseURL:          dsn,
			ClickHouseMigratorURL:  dsn,
			ClickHouseDB:           database,
			LogsTTL:                testTTL,
			TracesTTL:              testTTL,
			MetricsTTL:             testTTL,
			MigrationRetryBudget:   sharedRetryBudget,
			MigrationRetryInterval: testRetryInterval,
			MigrationLock:          config.MigrationLockKubernetes,
			MigrationLease: migrationlock.Config{
				Name:          leaseName,
				Namespace:     "default",
				Identity:      identity,
				LeaseDuration: testLeaseDuration,
				RenewDeadline: testRenewDeadline,
				RetryPeriod:   testRetryPeriod,
			},
			MaxConcurrent: testMaxConcurrent,
			TokenCacheTTL: testTokenTTL,
		}
		p, err := newProxy(cfg, migrationlock.New(client, cfg.MigrationLease))
		if err != nil {
			t.Fatalf("newProxy(%s): %v", identity, err)
		}
		t.Cleanup(p.close)
		var lc net.ListenConfig
		ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		bases = append(bases, "http://"+ln.Addr().String())
		go func() { runErrs <- p.run(ctx, cfg, ln) }()
	}
	for _, base := range bases {
		waitReady(t, base)
	}
	stop()
	for range bases {
		if err := <-runErrs; err != nil {
			t.Errorf("run: %v", err)
		}
	}

	var rows, versions uint64
	query := "SELECT count(), uniqExact(version_id) FROM " + database + "." + migrations.VersionTable
	if err := admin.QueryRow(t.Context(), query).Scan(&rows, &versions); err != nil {
		t.Fatalf("count version rows: %v", err)
	}
	t.Logf("version table: %d rows for %d versions", rows, versions)
	if rows != versions {
		t.Fatalf("version table has %d rows for %d versions, want each migration recorded once", rows, versions)
	}
}
