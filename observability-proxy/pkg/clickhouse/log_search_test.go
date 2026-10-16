package clickhouse

import (
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

func TestQueryLogsSearchMatchesLiteralText(t *testing.T) {
	f := newQueryFixture(t)
	bodies := []string{
		`Payment 100% complete; status_ok; C:\temp\record`,
		`Payment 1000 complete; statusXok; C:temprecord`,
		`trailing\`,
		"café_東京 50%",
		"caféX東京 500",
	}
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_logs ("+logsInsertColumns+")")
	if err != nil {
		t.Fatal(err)
	}
	for index, body := range bodies {
		for _, tenant := range []struct{ workspaceID, resourceID string }{
			{testWorkspaceID, testResourceID},
			{testOtherWorkspaceID, testResourceID},
			{testWorkspaceID, testWorkerResourceID},
		} {
			attributes := tenantAttributes(tenant.workspaceID, testEnvironmentID, tenant.resourceID)
			timestamp := f.end.Add(time.Duration(index) * time.Nanosecond)
			if appendErr := batch.Append(
				timestamp, testSeverity, body, attributes, "", "", map[string]string{},
			); appendErr != nil {
				t.Fatal(appendErr)
			}
		}
	}
	if sendErr := batch.Send(); sendErr != nil {
		t.Fatal(sendErr)
	}
	f.start = f.end
	f.end = f.start.Add(time.Duration(len(bodies)) * time.Nanosecond)
	for _, order := range []observabilityv1.LogOrder{
		observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
		observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST,
	} {
		t.Run(order.String(), func(t *testing.T) {
			for _, tc := range []struct{ name, search string }{
				{"percent", "%"},
				{"percent phrase", "100%"},
				{"underscore", "status_ok"},
				{"backslash", `\`},
				{"unicode", "café_東京"},
				{"case sensitive", "CAFÉ"},
				{"missing", "not-present"},
				{"empty", ""},
				{"ordinary", "record"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var want []string
					for _, body := range bodies {
						if strings.Contains(body, tc.search) {
							want = append(want, body)
						}
					}
					if order == observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST {
						slices.Reverse(want)
					}
					got := f.allPages(t, logQuery{
						search: tc.search, workspaceID: testWorkspaceID, resourceIDs: []string{testResourceID},
						limit: testPageSize, order: order,
					})
					if !slices.Equal(got, want) {
						t.Fatalf("search=%q returned %q, want %q", tc.search, got, want)
					}
				})
			}
		})
	}
}
