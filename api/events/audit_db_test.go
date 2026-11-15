package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/internal/logquery"
)

func TestAuditFilterScanBounds(t *testing.T) {
	pool := dbtest.NewPool(t)
	org, workspace, resource, environment := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(t.Context(), `
INSERT INTO events (type, org_id, workspace_id, resource_id, environment_id, actor_type, created_at)
SELECT CASE WHEN g = 1 THEN 'rare' ELSE 'common' END,
       $1, $2, $3, CASE WHEN g = 1 THEN $4::uuid END, 'system',
       '2026-01-01'::timestamptz + (50001 - g) * interval '1 second'
FROM generate_series(1, 50000) g`, org, workspace, resource, environment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "ANALYZE events"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, scope := range []struct {
		name                    string
		workspaceID, resourceID *uuid.UUID
	}{
		{"organization", nil, nil},
		{"workspace", &workspace, nil},
		{"resource", &workspace, &resource},
	} {
		for _, filter := range []struct {
			name        string
			environment *uuid.UUID
			start, end  *time.Time
			query       string
			want        int
		}{
			{name: "rare type", query: "@type:rare", want: 1},
			{name: "multiple types", query: "@type:rare OR @type:absent", want: 1},
			{name: "absent type", query: "@type:absent"},
			{name: "environment", environment: &environment, want: 1},
			{name: "time window", start: new(at.Add(25000 * time.Second)),
				end: new(at.Add(25100 * time.Second)), want: 100},
			{name: "start", start: new(at.Add(49901 * time.Second)), want: 100},
			{name: "end", end: new(at.Add(101 * time.Second)), want: 100},
		} {
			t.Run(scope.name+"/"+filter.name, func(t *testing.T) {
				expr, err := logquery.Parse(filter.query)
				if err != nil {
					t.Fatal(err)
				}
				sql, args := auditSQL(AuditQuery{
					OrgID: org, WorkspaceID: scope.workspaceID, ResourceID: scope.resourceID,
					EnvironmentID: filter.environment, Start: filter.start, End: filter.end, Filter: expr, Limit: 101,
				})
				var raw []byte
				if err := pool.QueryRow(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args...).
					Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var plans []struct {
					Plan auditScanPlan `json:"Plan"`
				}
				if err := json.Unmarshal(raw, &plans); err != nil {
					t.Fatal(err)
				}
				if plans[0].Plan.Rows != float64(filter.want) {
					t.Fatalf("rows = %.0f, want %d: %s", plans[0].Plan.Rows, filter.want, raw)
				}
				if scanned := plans[0].Plan.eventsScanned(); scanned > 1000 {
					t.Fatalf("examined %.0f events for %d results: %s", scanned, filter.want, raw)
				}
			})
		}
	}
}

type auditScanPlan struct {
	Relation string          `json:"Relation Name"`
	Rows     float64         `json:"Actual Rows"`
	Removed  float64         `json:"Rows Removed by Filter"`
	Loops    float64         `json:"Actual Loops"`
	Plans    []auditScanPlan `json:"Plans"`
}

func (p auditScanPlan) eventsScanned() float64 {
	n := float64(0)
	if p.Relation == "events" {
		n = (p.Rows + p.Removed) * p.Loops
	}
	for _, child := range p.Plans {
		n += child.eventsScanned()
	}
	return n
}
