package ingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

var errTableMissing = errors.New("table does not exist")

// CheckSchema checks that conn can see every table the writer inserts into.
func CheckSchema(ctx context.Context, conn driver.Conn, database string, tables []string) error {
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
