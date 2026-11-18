package sweepers

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPlacementWindowSweepDeletesWindowsClosedLongerThanKeep(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const keep = time.Hour
	now := time.Now()
	insert := func(placed time.Time, removed *time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := f.pool.QueryRow(f.ctx, `
INSERT INTO placement_windows (resource_id, cluster_id, workspace_id, placed_at, removed_at)
VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			uuid.New(), f.clusterID, f.workspaceID, placed, removed,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	at := func(ago time.Duration) *time.Time {
		moment := now.Add(-ago)
		return &moment
	}
	open := insert(*at(10 * keep), nil)
	recent := insert(*at(10 * keep), at(keep-time.Minute))
	insert(*at(10 * keep), at(keep+time.Minute))

	result, err := NewPlacementWindowSweeper(f.pool, time.Minute, keep).Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || result.WindowsDeleted != 1 {
		t.Fatalf("sweep = %+v, want one window deleted", result)
	}
	rows, err := f.pool.Query(f.ctx, "SELECT id FROM placement_windows ORDER BY removed_at NULLS FIRST")
	if err != nil {
		t.Fatal(err)
	}
	var kept []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			t.Fatal(scanErr)
		}
		kept = append(kept, id)
	}
	if !reflect.DeepEqual(kept, []uuid.UUID{open, recent}) {
		t.Fatalf("kept %v, want the open and the recently closed window", kept)
	}
}
