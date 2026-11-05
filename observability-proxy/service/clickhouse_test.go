package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/observability-proxy/migrations"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const (
	testClickHouseConns = 2
	testQueryTimeout    = 10
	testLogsTTL         = 48 * time.Hour
	testTelemetryTTL    = time.Hour
)

func testClickHouseDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	return dsn
}

func newTestClickHouse(t *testing.T) *chClient.Client {
	t.Helper()
	dsn := testClickHouseDSN(t)
	database := "service_" + fmt.Sprintf("%x", uuid.New())
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    testLogsTTL,
		TracesTTL:  testTelemetryTTL,
		MetricsTTL: testTelemetryTTL,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	client, err := chClient.NewClient(dsn, database, testClickHouseConns, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); err != nil {
			t.Error(err)
		}
		client.Close()
	})
	return client
}

// every query through this client fails, so a request that reaches ClickHouse ends with an
// internal error instead of the code its own validation or authorization would return.
func unavailableClickHouse(t *testing.T) *chClient.Client {
	t.Helper()
	database := "missing_" + fmt.Sprintf("%x", uuid.New())
	client, err := chClient.NewClient(testClickHouseDSN(t), database, testClickHouseConns, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}
