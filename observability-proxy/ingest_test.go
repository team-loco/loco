package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"

	"github.com/team-loco/loco/internal/clickhouse/clickhousetest"
	"github.com/team-loco/loco/internal/clickhouse/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const (
	testOTLPMaxBytes      = 1 << 20
	testIngestTimeout     = time.Minute
	testIngestDialTimeout = 10 * time.Second
	ingestOnlyLogBody     = "written by the ingest server"
)

func localListener(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func postLog(t *testing.T, endpoint string) int {
	t.Helper()
	logs := plog.NewLogs()
	record := logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	now := time.Now()
	record.SetTimestamp(pcommon.NewTimestampFromTime(now))
	record.Body().SetStr(ingestOnlyLogBody)
	exportRequest := plogotlp.NewExportRequestFromLogs(logs)
	body, err := exportRequest.MarshalProto()
	if err != nil {
		t.Fatalf("marshal logs: %v", err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	return response.StatusCode
}

func TestIngestMigratesWithItsMigratorAndRefusesUnauthenticatedTelemetry(t *testing.T) {
	db := clickhousetest.New(t, databasePrefix)
	cfg := testConfig(db.User(t, "INSERT, SHOW COLUMNS"), db.DSN, db.Name, sharedRetryBudget)
	p, err := newProxy(cfg, lockFor(cfg), testVersion)
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)
	ln := testListeners(t, localListener(t))
	base := "http://" + ln.main.Addr().String()
	ctx, stop := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() { runErr <- p.run(ctx, cfg, ln) }()

	waitReady(t, base)
	var versions uint64
	query := "SELECT uniqExact(version_id) FROM " + db.Name + "." + migrations.VersionTable
	if err := db.Admin.QueryRow(t.Context(), query).Scan(&versions); err != nil || versions == 0 {
		t.Fatalf("applied migrations = %d, %v; want the schema migrated", versions, err)
	}
	endpoint := "http://" + ln.otlpHTTP.Addr().String() + ingest.LogsPath
	if status := postLog(t, endpoint); status != http.StatusUnauthorized {
		t.Fatalf("POST %s without an ingest token = %d, want %d", endpoint, status, http.StatusUnauthorized)
	}
	var rows uint64
	query = "SELECT count() FROM " + db.Name + ".otel_logs WHERE Body = ?"
	if err := db.Admin.QueryRow(t.Context(), query, ingestOnlyLogBody).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("otel_logs rows = %d, want none", rows)
	}

	stop()
	if err := <-runErr; err != nil {
		t.Fatalf("run: %v", err)
	}
}
