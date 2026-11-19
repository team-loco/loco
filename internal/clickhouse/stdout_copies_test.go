package clickhouse

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	exportingPodUID = "pod-exporting"
	stdoutPodUID    = "pod-stdout"
	stoppedPodUID   = "pod-stopped"
	otlpBody        = "GetCart called with userId={userId}"
	stdoutCopyBody  = "info: GetCart called with userId=42"
	stderrBody      = "panic: boom"
	otherStdoutBody = "GET /health 200"
	stoppedOTLPBody = "exported before the window"
	stoppedBody     = "printed after exporting stopped"
	stdoutStream    = "stdout"
)

func (f *queryFixture) insertPodLogs(t *testing.T, window time.Time) {
	t.Helper()
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_logs ("+logsInsertColumns+")")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		pod, stream, body string
		offset            time.Duration
	}{
		{exportingPodUID, "", otlpBody, time.Second},
		{exportingPodUID, stdoutStream, stdoutCopyBody, 2 * time.Second},
		{exportingPodUID, "stderr", stderrBody, 3 * time.Second},
		{stdoutPodUID, stdoutStream, otherStdoutBody, 2 * time.Second},
		{stoppedPodUID, "", stoppedOTLPBody, -time.Minute},
		{stoppedPodUID, stdoutStream, stoppedBody, 4 * time.Second},
	} {
		attributes := tenantAttributes(testUnknownWorkspaceID, testEnvironmentID, testResourceID)
		attributes["k8s.pod.uid"] = row.pod
		record := map[string]string{}
		if row.stream != "" {
			record["log.iostream"] = row.stream
		}
		if err := batch.Append(window.Add(row.offset), testSeverity, row.body, attributes, "", "", record); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
}

func TestLogQueriesHideStdoutCopiesOfOTLPLogs(t *testing.T) {
	f := newQueryFixture(t)
	window := f.end
	f.insertPodLogs(t, window)
	for _, test := range []struct {
		name  string
		start time.Time
		want  []string
	}{
		{"pod stopped exporting before the window", window, []string{
			otlpBody, otherStdoutBody, stderrBody, stoppedBody,
		}},
		{"window covers every export", window.Add(-2 * time.Minute), []string{
			stoppedOTLPBody, otlpBody, otherStdoutBody, stderrBody,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := LogQuery{
				WorkspaceID: testUnknownWorkspaceID, Start: test.start, End: window.Add(time.Minute),
				Limit: testFullPage, Order: observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
			}
			page, err := f.client.QueryLogs(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			var bodies []string
			for _, entry := range page.GetEntries() {
				bodies = append(bodies, entry.GetBody())
			}
			if !slices.Equal(bodies, test.want) || page.GetTotalMatched() != int64(len(test.want)) {
				t.Fatalf("logs %q (total %d), want %q", bodies, page.GetTotalMatched(), test.want)
			}
			buckets, err := f.client.QueryLogBuckets(t.Context(), q, 3600,
				observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_LEVEL)
			if err != nil {
				t.Fatal(err)
			}
			var counted uint64
			for _, bucket := range buckets.GetBuckets() {
				counted += bucket.GetCount()
			}
			if counted != uint64(len(test.want)) {
				t.Fatalf("buckets count %d, want %d", counted, len(test.want))
			}
			tail, err := f.client.QueryTailLogPage(t.Context(), q, TailLogCursor{})
			if err != nil {
				t.Fatal(err)
			}
			if len(tail.Entries) != len(test.want) {
				t.Fatalf("tail returned %d rows, want %d", len(tail.Entries), len(test.want))
			}
		})
	}
}

func TestUnfilteredLogBucketsReadNoAttributeMaps(t *testing.T) {
	f := newQueryFixture(t)
	f.insertPodLogs(t, f.end)
	comment := t.Name() + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := clickhouse.Context(t.Context(), clickhouse.WithSettings(clickhouse.Settings{"log_comment": comment}))
	q := LogQuery{WorkspaceID: testUnknownWorkspaceID, Start: f.start, End: f.end.Add(time.Minute)}
	group := observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_LEVEL
	if _, err := f.client.QueryLogBuckets(ctx, q, 3600, group); err != nil {
		t.Fatal(err)
	}
	if err := f.conn.Exec(t.Context(), "SYSTEM FLUSH LOGS"); err != nil {
		t.Fatal(err)
	}
	var columns []string
	if err := f.conn.QueryRow(t.Context(), "SELECT groupUniqArrayArray(columns) FROM system.query_log "+
		"WHERE log_comment = ? AND type = 'QueryFinish'", comment).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(columns, func(column string) bool { return strings.HasSuffix(column, ".Timestamp") }) ||
		slices.ContainsFunc(columns, func(column string) bool { return strings.Contains(column, "Attributes") }) {
		t.Fatalf("log buckets read columns %q, want Timestamp and no attribute map", columns)
	}
}
