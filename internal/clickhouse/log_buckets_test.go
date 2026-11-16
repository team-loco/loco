package clickhouse

import (
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const bucketErrorSeverity = "ERROR"

func TestLogBuckets(t *testing.T) {
	f := newQueryFixture(t)
	start := f.start.Truncate(time.Minute).Add(time.Minute)
	const marker = "bucket-marker"
	const fullCount = 10005
	for _, row := range []struct {
		workspace, environment, resource, severity string
		offset                                     time.Duration
		count                                      uint64
	}{
		{testWorkspaceID, testEnvironmentID, testResourceID, testSeverity, 0, fullCount},
		{testWorkspaceID, testEnvironmentID, testWorkerResourceID, bucketErrorSeverity, time.Minute, 2},
		{testOtherWorkspaceID, testEnvironmentID, testResourceID, testSeverity, 0, 7},
		{testWorkspaceID, testOtherEnvironmentID, testResourceID, testSeverity, 0, 3},
		{testWorkspaceID, testEnvironmentID, testResourceID, testSeverity, 3 * time.Minute, 9},
		{testWorkspaceID, testEnvironmentID, testResourceID, testSeverity, -time.Minute, 11},
	} {
		err := f.conn.Exec(t.Context(), "INSERT INTO otel_logs (Timestamp, Body, SeverityText, ResourceAttributes) "+
			"SELECT ?, ?, ?, ? FROM numbers(?)", start.Add(row.offset), marker, row.severity,
			tenantAttributes(row.workspace, row.environment, row.resource), row.count)
		if err != nil {
			t.Fatal(err)
		}
	}
	q := LogQuery{WorkspaceID: testWorkspaceID, EnvironmentID: testEnvironmentID, Start: start,
		End: start.Add(2 * time.Minute), Filter: mustParse(t, marker), Limit: 1}
	page, err := f.client.QueryLogs(t.Context(), q)
	if err != nil || len(page.GetEntries()) != 1 || page.GetNextCursor() == "" {
		t.Fatalf("page: %v, %v", page, err)
	}
	for _, group := range []observabilityv1.LogBucketGroup{
		observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_SEVERITY,
		observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_RESOURCE,
	} {
		result, queryErr := f.client.QueryLogBuckets(t.Context(), q, 60, group)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		buckets := result.GetBuckets()
		if len(buckets) != 2 || buckets[0].GetCount() != fullCount || buckets[1].GetCount() != 2 ||
			!buckets[0].GetStartTime().
				AsTime().
				Equal(start) ||
			!buckets[1].GetStartTime().AsTime().Equal(start.Add(time.Minute)) {
			t.Fatalf("group %v: %v", group, result)
		}
		if group == observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_SEVERITY {
			if buckets[0].GetSeverity() != testSeverity || buckets[1].GetSeverity() != bucketErrorSeverity {
				t.Fatalf("severities: %v", result)
			}
		} else if buckets[0].GetResourceId() != testResourceID || buckets[1].GetResourceId() != testWorkerResourceID {
			t.Fatalf("resources: %v", result)
		}
	}
	q.ResourceIDs = []string{testWorkerResourceID}
	result, err := f.client.QueryLogBuckets(
		t.Context(),
		q,
		120,
		observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_RESOURCE,
	)
	if err != nil || len(result.GetBuckets()) != 1 || result.GetBuckets()[0].GetCount() != 2 {
		t.Fatalf("resource scope: %v, %v", result, err)
	}
	q.Filter = mustParse(t, "missing-marker")
	result, err = f.client.QueryLogBuckets(t.Context(), q, 60, observabilityv1.LogBucketGroup_LOG_BUCKET_GROUP_SEVERITY)
	if err != nil || len(result.GetBuckets()) != 0 {
		t.Fatalf("empty range: %v, %v", result, err)
	}
}
