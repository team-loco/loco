package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
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
		name  string
		query string
		ids   []any
	}{
		{"organization", listOrgAuditEvents, []any{org}},
		{"workspace", listWorkspaceAuditEvents, []any{org, workspace}},
		{"resource", listResourceAuditEvents, []any{org, workspace, resource}},
	} {
		for _, filter := range []struct {
			name        string
			environment any
			start, end  any
			types       []string
			want        int
		}{
			{name: "rare type", types: []string{"rare"}, want: 1},
			{name: "multiple types", types: []string{"rare", "absent"}, want: 1},
			{name: "absent type", types: []string{"absent"}},
			{name: "environment", environment: environment, types: []string{}, want: 1},
			{name: "time window", start: at.Add(25000 * time.Second),
				end: at.Add(25100 * time.Second), types: []string{}, want: 100},
			{name: "start", start: at.Add(49901 * time.Second), types: []string{}, want: 100},
			{name: "end", end: at.Add(101 * time.Second), types: []string{}, want: 100},
		} {
			t.Run(scope.name+"/"+filter.name, func(t *testing.T) {
				args := append([]any{}, scope.ids...)
				args = append(args, filter.environment, filter.start, filter.end, nil, filter.types, nil, nil, "", 101)
				var raw []byte
				err := pool.QueryRow(
					t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+scope.query, args...,
				).Scan(&raw)
				if err != nil {
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
