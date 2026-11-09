package db

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

func newTestPool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := NewPool(t.Context(), Config{URL: databaseURL, MaxConns: 2, StatementTimeout: 1500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestPoolSetsTheStatementTimeout(t *testing.T) {
	databaseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	var timeout string
	if err := newTestPool(t, databaseURL).QueryRow(t.Context(), "SHOW statement_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != "1500ms" {
		t.Fatalf("statement_timeout = %q, want 1500ms", timeout)
	}
}

func TestPoolRecordsQuerySpansUnderTheCaller(t *testing.T) {
	databaseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	recorder := tracingtest.Record(t)
	pool := newTestPool(t, databaseURL)
	ctx := tracingtest.StartParent(t)
	if _, execErr := pool.Exec(ctx, "SELECT 1"); execErr != nil {
		t.Fatal(execErr)
	}
	parentID := trace.SpanContextFromContext(ctx).SpanID()
	for _, span := range recorder.Ended() {
		if span.Parent().SpanID() == parentID {
			return
		}
	}
	t.Fatal("the query recorded no span under the caller's span")
}
