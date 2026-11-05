package clickhouse

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"
)

const tailTestCursorBlocks = 20000

func TestTailPageFiltersDuplicatesAndLargeCursorAcrossMerges(t *testing.T) {
	f := newQueryFixture(t)
	const search = "tail ' ? {tail_seen:String} %_"
	timestamp := f.end.Add(time.Nanosecond)
	attrs := tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)
	attrs[testSharedLabelKey] = search
	attrs[regionAttribute] = testRegion
	batch, err := f.conn.PrepareBatch(
		t.Context(),
		"INSERT INTO otel_logs (Timestamp, SeverityText, Body, ResourceAttributes)",
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		key, value, severity, body string
		at                         time.Time
	}{
		{at: timestamp}, {at: timestamp}, {at: timestamp},
		{at: timestamp.Add(time.Hour)},
		{key: workspaceAttributeKey, value: testOtherWorkspaceID, at: timestamp},
		{key: environmentAttrKey, value: testOtherEnvironmentID, at: timestamp},
		{key: environmentAttrKey, value: "", at: timestamp},
		{body: strings.Replace(search, "%_", "expanded", 1), at: timestamp},
		{key: resourceAttributeKey, value: testWorkerResourceID, at: timestamp},
		{key: regionAttribute, value: testWorkerRegion, at: timestamp},
		{key: regionAttribute, value: "us-west-test", at: timestamp},
		{key: testSharedLabelKey, value: "other", at: timestamp},
		{severity: "ERROR", at: timestamp},
		{at: timestamp.Add(-time.Nanosecond)},
	}
	for _, row := range rows {
		copyAttrs := maps.Clone(attrs)
		if row.key != "" {
			copyAttrs[row.key] = row.value
		}
		severity := row.severity
		if severity == "" {
			severity = testSeverity
		}
		body := row.body
		if body == "" {
			body = search
		}
		if appendErr := batch.Append(row.at, severity, body, copyAttrs); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	if sendErr := batch.Send(); sendErr != nil {
		t.Fatal(sendErr)
	}
	query := LogQuery{
		WorkspaceID:     testWorkspaceID,
		EnvironmentID:   testEnvironmentID,
		ResourceIDs:     []string{testResourceID},
		Start:           timestamp,
		Search:          search,
		Levels:          []string{testSeverity},
		Labels:          map[string]string{testSharedLabelKey: search},
		Regions:         []string{testUSRegionPattern},
		ExcludedRegions: []string{"us-west-*"},
		Limit:           testFullPage,
	}
	large := TailLogCursor{}
	for i := range tailTestCursorBlocks {
		large[fmt.Sprintf("other/%d", i)] = uint64(i)
	}
	page, err := f.client.QueryTailLogPage(t.Context(), query, large)
	if err != nil {
		t.Fatal(err)
	}
	const expected = 4
	if len(page.Entries) != expected || page.More {
		t.Fatalf("large cursor page=%d entries, more=%v", len(page.Entries), page.More)
	}
	cursor := TailLogCursor{}
	query.Limit = 1
	var count int
	for i := range expected {
		page, err = f.client.QueryTailLogPage(t.Context(), query, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) != 1 {
			t.Fatalf("page %d entries=%d", i, len(page.Entries))
		}
		entry := page.Entries[0]
		if entry.GetBody() != search || entry.GetResourceId() != testResourceID ||
			entry.GetResourceAttributes()[workspaceAttributeKey] != testWorkspaceID {
			t.Fatalf("unexpected record: %v", entry)
		}
		count += len(page.Entries)
		maps.Copy(cursor, page.Blocks)
		if page.More != (i < expected-1) {
			t.Fatalf("page %d more=%v", i, page.More)
		}
		if i == 0 {
			if mergeErr := f.conn.Exec(t.Context(), "OPTIMIZE TABLE otel_logs FINAL"); mergeErr != nil {
				t.Fatal(mergeErr)
			}
		}
	}
	page, err = f.client.QueryTailLogPage(t.Context(), query, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 || count != expected {
		t.Fatalf("delivered=%d, repeated=%d", count, len(page.Entries))
	}
}
