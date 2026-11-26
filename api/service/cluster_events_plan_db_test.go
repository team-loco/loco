package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
)

type clusterEventsTrace struct {
	query string
	args  []any
}

func (tracer *clusterEventsTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	tracer.query, tracer.args = data.SQL, data.Args
	return ctx
}

func (*clusterEventsTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {}

type clusterEventPlan struct {
	Relation  string             `json:"Relation Name"`
	Rows      float64            `json:"Actual Rows"`
	Loops     float64            `json:"Actual Loops"`
	Filtered  float64            `json:"Rows Removed by Filter"`
	Rechecked float64            `json:"Rows Removed by Index Recheck"`
	Children  []clusterEventPlan `json:"Plans"`
}

func (plan clusterEventPlan) eventRowsExamined() float64 {
	var rows float64
	if plan.Relation == "events" {
		rows = plan.Loops * (plan.Rows + plan.Filtered + plan.Rechecked)
	}
	for _, child := range plan.Children {
		rows += child.eventRowsExamined()
	}
	return rows
}

func TestQuietClusterActivityDoesNotReadUnrelatedEventRows(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO events(type, actor_type, subject_type, subject_id)
SELECT 'cluster.health_changed', 'agent', 'cluster', $1 FROM generate_series(1, 20000)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), "ANALYZE events"); err != nil {
		t.Fatal(err)
	}
	tracer := &clusterEventsTrace{}
	cfg := f.pool.Config()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := genDb.New(pool).ListClusterEvents(t.Context(), &f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("quiet cluster events = %v", rows)
	}
	if tracer.query == "" {
		t.Fatal("cluster event query was not captured")
	}
	var raw []byte
	if err := f.pool.QueryRow(t.Context(), "EXPLAIN (ANALYZE, FORMAT JSON) "+tracer.query, tracer.args...).
		Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plans []struct {
		Plan clusterEventPlan `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("query plans = %d, want 1", len(plans))
	}
	examined := plans[0].Plan.eventRowsExamined()
	t.Logf("quiet cluster examined %.0f event rows with 20000 unrelated events retained", examined)
	if examined != 0 {
		t.Fatalf("quiet cluster read %.0f unrelated event rows", examined)
	}
}
