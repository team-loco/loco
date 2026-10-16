package migrations

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMetricsPreserveNanosecondTimestamps(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	for _, insert := range exporterInserts() {
		if !strings.HasPrefix(insert.table, "otel_metrics_") {
			continue
		}
		t.Run(insert.table, func(t *testing.T) {
			table := db.cfg.Database + "." + insert.table
			columns := strings.Join(insert.columns, ", ")
			batch, err := db.admin.PrepareBatch(t.Context(), "INSERT INTO "+table+" ("+columns+")")
			if err != nil {
				t.Fatal(err)
			}
			row := insert.row(timestamp)
			if err := batch.Append(row...); err != nil {
				t.Fatal(err)
			}
			if err := batch.Send(); err != nil {
				t.Fatal(err)
			}
			var start, sample time.Time
			query := "SELECT StartTimeUnix, TimeUnix FROM " + table
			if err := db.admin.QueryRow(t.Context(), query).Scan(&start, &sample); err != nil {
				t.Fatal(err)
			}
			if !start.Equal(timestamp) || !sample.Equal(timestamp) {
				t.Errorf("start=%v sample=%v, want %v", start, sample, timestamp)
			}
			if slices.Contains(insert.columns, "Exemplars.TimeUnix") {
				var exemplars []time.Time
				query = "SELECT `Exemplars.TimeUnix` FROM " + table
				if err := db.admin.QueryRow(t.Context(), query).Scan(&exemplars); err != nil {
					t.Fatal(err)
				}
				if !slices.EqualFunc(exemplars, []time.Time{timestamp}, time.Time.Equal) {
					t.Errorf("exemplar times=%v, want %v", exemplars, timestamp)
				}
			}
		})
	}
}
