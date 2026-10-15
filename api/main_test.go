package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3/lock"
	"github.com/team-loco/loco/api/auth/authtest"
)

const configuredOrigin = "https://app.loco.build"

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

func TestWithCORSExposedHeaders(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	h := withCORS([]string{configuredOrigin}, false)(next)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/loco.user.v1.UserService/WhoAmI", nil)
	req.Header.Set("Origin", configuredOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	exposed := strings.ToLower(strings.Join(rec.Header().Values("Access-Control-Expose-Headers"), ","))
	if !strings.Contains(exposed, "x-loco-request-id") {
		t.Errorf("exposed headers %q missing x-loco-request-id", exposed)
	}
	if strings.Contains(exposed, "server-timing") {
		t.Errorf("exposed headers %q include server-timing", exposed)
	}
}

const apiProcessEnv = "LOCO_TEST_API_PROCESS"

func TestAPIProcess(t *testing.T) {
	if os.Getenv(apiProcessEnv) != "1" {
		t.Skip("subprocess helper")
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append(os.Args[:1], os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing argument separator")
}

func TestAPIRejectsMigrateCommand(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv(apiProcessEnv, "1")
	t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/loco?connect_timeout=1")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := apiTestCommand(t, ctx, "migrate")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("migrate command succeeded")
	}
	if !strings.Contains(string(output), `unexpected argument "migrate"`) {
		t.Fatalf("output = %s, want rejection before database access", output)
	}
}

func TestAPIStartupWaitsForMigrations(t *testing.T) {
	pool := authtest.NewPool(t)
	clearAPIConfigEnv(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration lock connection: %v", err)
	}
	defer conn.Release()
	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lock.DefaultLockID); lockErr != nil {
		t.Fatalf("hold migration lock: %v", lockErr)
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.Background(), "SELECT pg_advisory_unlock_all()"); unlockErr != nil {
			t.Errorf("release migration lock: %v", unlockErr)
		}
	}()
	addr := apiTestAddress(t, ctx)
	databaseURL, parseErr := url.Parse(pool.Config().ConnString())
	if parseErr != nil {
		t.Fatalf("parse database url: %v", parseErr)
	}
	params := databaseURL.Query()
	params.Set("application_name", "api-startup-test")
	databaseURL.RawQuery = params.Encode()
	t.Setenv("DATABASE_URL", databaseURL.String())
	t.Setenv("APP_PORT", addr)
	t.Setenv(apiProcessEnv, "1")
	cmd := apiTestCommand(t, ctx)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if startErr := cmd.Start(); startErr != nil {
		t.Fatalf("start api: %v", startErr)
	}
	defer func() {
		if signalErr := cmd.Process.Signal(os.Interrupt); signalErr != nil {
			t.Errorf("interrupt api: %v", signalErr)
			cancel()
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			t.Errorf("wait for api: %v", waitErr)
		}
		if t.Failed() {
			t.Logf("api output: %s", output.String())
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		queryErr := pool.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM pg_stat_activity
 WHERE application_name = 'api-startup-test' AND query LIKE '%pg_try_advisory_lock%'
 )`).Scan(&waiting)
		if queryErr != nil {
			t.Fatalf("check startup migration lock attempt: %v", queryErr)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("api never attempted the migration lock")
		case <-ticker.C:
		}
	}
	client := &http.Client{Timeout: time.Second}
	req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if requestErr != nil {
		t.Fatalf("create health request: %v", requestErr)
	}
	resp, err := client.Do(req)
	if err == nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close health response: %v", closeErr)
		}
		t.Fatal("api served before migrations acquired their lock")
	}
	var schemaExists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public.users') IS NOT NULL").Scan(&schemaExists); err != nil {
		t.Fatalf("check unmigrated schema: %v", err)
	}
	if schemaExists {
		t.Fatal("schema changed while another replica held the migration lock")
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock_all()"); err != nil {
		t.Fatalf("release migration lock: %v", err)
	}
	for {
		resp, err := client.Do(req)
		if err == nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Errorf("close health response: %v", closeErr)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("health status = %d", resp.StatusCode)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("api never served after the migration lock was released")
		case <-ticker.C:
		}
	}
	if err := pool.QueryRow(ctx, "SELECT to_regclass('public.users') IS NOT NULL").Scan(&schemaExists); err != nil {
		t.Fatalf("check migrated schema: %v", err)
	}
	if !schemaExists {
		t.Fatal("api served without applying migrations")
	}
}

func apiTestCommand(t *testing.T, ctx context.Context, args ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find api test executable: %v", err)
	}
	processArgs := append([]string{"-test.run=^TestAPIProcess$", "--"}, args...)
	return exec.CommandContext(ctx, executable, processArgs...)
}

func apiTestAddress(t *testing.T, ctx context.Context) string {
	t.Helper()
	listenConfig := &net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve api address: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release api address: %v", err)
	}
	return addr
}
