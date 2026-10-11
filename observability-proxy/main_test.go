package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/config"
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
)

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
		p, err := newProxy(cfg)
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
		ClickHouseMigratorURL:  "clickhouse://127.0.0.1:1",
		ClickHouseDB:           "loco_obs",
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   testRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg)
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
		ClickHouseMigratorURL:  "clickhouse://127.0.0.1:1",
		ClickHouseDB:           "loco_obs",
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   testRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg)
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
