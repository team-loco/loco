package clickhouse

import (
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func TestLogLevelAgreesAcrossFilterBucketsAndEntries(t *testing.T) {
	const (
		debug = observabilityv1.LogLevel_LOG_LEVEL_DEBUG
		info  = observabilityv1.LogLevel_LOG_LEVEL_INFO
		warn  = observabilityv1.LogLevel_LOG_LEVEL_WARN
		fail  = observabilityv1.LogLevel_LOG_LEVEL_ERROR
	)
	f := newQueryFixture(t)
	const marker = "level-marker"
	rows := []struct {
		text   string
		number uint8
		level  observabilityv1.LogLevel
	}{
		{testSeverity, 9, info}, {"info", 0, info}, {"Information", 0, info}, {testSeverity, 0, info},
		{"", 0, info}, {"custom", 0, info}, {"", 10, info}, {bucketErrorSeverity, 9, info},
		{"WARN", 13, warn}, {"Warning", 0, warn}, {"warn", 0, warn}, {"", 14, warn},
		{bucketErrorSeverity, 17, fail}, {"Error", 0, fail}, {"error", 0, fail}, {"FATAL", 21, fail},
		{"critical", 0, fail}, {"", 17, fail}, {testSeverity, 18, fail},
		{"debug", 0, debug}, {"TRACE", 1, debug}, {"", 5, debug},
	}
	batch, err := f.conn.PrepareBatch(t.Context(),
		"INSERT INTO otel_logs (Timestamp, SeverityText, SeverityNumber, Body, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	want := map[observabilityv1.LogLevel]uint64{}
	attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	for i, row := range rows {
		at := f.start.Add(time.Duration(i) * time.Second)
		if appendErr := batch.Append(at, row.text, row.number, marker, attrs); appendErr != nil {
			t.Fatal(appendErr)
		}
		want[row.level]++
	}
	if err = batch.Send(); err != nil {
		t.Fatal(err)
	}

	base := LogQuery{WorkspaceID: testWorkspaceID, Start: f.start, End: f.end, Filter: mustParse(t, marker),
		Limit: testFullPage}
	buckets, err := f.client.QueryLogBuckets(t.Context(), base, int32(testDataAge.Seconds()),
		observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_LEVEL)
	if err != nil {
		t.Fatal(err)
	}
	got := map[observabilityv1.LogLevel]uint64{}
	for _, bucket := range buckets.GetBuckets() {
		got[bucket.GetLevel()] += bucket.GetCount()
	}
	for level, count := range want {
		if got[level] != count {
			t.Errorf("buckets count %d %v rows, want %d", got[level], level, count)
		}
	}

	for query, level := range map[string]observabilityv1.LogLevel{
		"@level:debug": debug, "@level:info": info, "@level:warn": warn, "@level:error": fail,
		"@level:Warning": warn, "@level:fatal": fail, "@level:err*": fail,
	} {
		q := base
		q.Filter = mustParse(t, marker+" "+query)
		page, err := f.client.QueryLogs(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		if uint64(len(page.GetEntries())) != want[level] || uint64(page.GetTotalMatched()) != want[level] {
			t.Errorf("%s returned %d of %d rows, want %d", query, len(page.GetEntries()), page.GetTotalMatched(),
				want[level])
		}
		for _, entry := range page.GetEntries() {
			if entry.GetLevel() != level {
				t.Errorf("%s returned a %v row", query, entry.GetLevel())
			}
		}
	}
}
