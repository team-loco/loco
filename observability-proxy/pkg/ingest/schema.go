package ingest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var errTableMissing = errors.New("table does not exist")

func missingTable(ctx context.Context, conn driver.Conn, database string, tables []string) error {
	for _, table := range tables {
		var exists uint8
		query := fmt.Sprintf("EXISTS TABLE %s.%s", database, table)
		if err := conn.QueryRow(ctx, query).Scan(&exists); err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
		if exists == 0 {
			return fmt.Errorf("%s.%s: %w", database, table, errTableMissing)
		}
	}
	return nil
}

func WaitForSchema(
	ctx context.Context,
	conn driver.Conn,
	database string,
	tables []string,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last error
	for {
		err := missingTable(ctx, conn, database, tables)
		if err == nil {
			return nil
		}
		if ctx.Err() == nil {
			last = err
		}
		select {
		case <-ctx.Done():
			cause := cmp.Or(last, err)
			return fmt.Errorf("wait for schema: %w", errors.Join(ctx.Err(), cause))
		case <-ticker.C:
		}
	}
}
