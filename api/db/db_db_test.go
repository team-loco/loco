package db

import (
	"os"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/pkg/tracingtest"
)

func TestPoolRecordsQuerySpansUnderTheCaller(t *testing.T) {
	databaseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	recorder := tracingtest.Record(t)
	conn, err := NewDB(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	ctx := tracingtest.StartParent(t)
	if _, execErr := conn.Pool().Exec(ctx, "SELECT 1"); execErr != nil {
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
