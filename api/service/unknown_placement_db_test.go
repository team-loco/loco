package service

import (
	"testing"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestRecordUnknownPlacementsPreservesFirstObservation(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	known := f.placement(t, f.clusterID)
	unknown := uuid.New()
	params := genDb.RecordUnknownPlacementsParams{
		ClusterID:    f.clusterID,
		PlacementIds: []uuid.UUID{known.ID, unknown},
		Revisions:    []int64{known.DesiredRevision, known.DesiredRevision},
	}
	rows, err := f.queries.RecordUnknownPlacements(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PlacementID != unknown || rows[0].FirstSeenAt.IsZero() {
		t.Fatalf("recorded = %+v, want only the unknown placement", rows)
	}
	first := rows[0]
	params.Revisions = []int64{known.DesiredRevision, known.DesiredRevision + 1}
	repeated, err := f.queries.RecordUnknownPlacements(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated) != 0 {
		t.Fatalf("repeat inventory recorded new placements: %+v", repeated)
	}
	var current genDb.RecordUnknownPlacementsRow
	if err := f.pool.QueryRow(t.Context(),
		`SELECT placement_id, reported_revision, first_seen_at FROM cluster_unknown_placements
		 WHERE cluster_id = $1 AND placement_id = $2`, f.clusterID, unknown,
	).Scan(&current.PlacementID, &current.ReportedRevision, &current.FirstSeenAt); err != nil {
		t.Fatal(err)
	}
	if current != first {
		t.Fatalf("first observation changed: %+v, want %+v", current, first)
	}
}

func TestUnknownPlacementsAreScopedToTheirCluster(t *testing.T) {
	f := newDeployFixture(t)
	unknown := uuid.New()
	for _, clusterID := range []uuid.UUID{f.clusterID, f.otherCluster} {
		params := genDb.RecordUnknownPlacementsParams{
			ClusterID:    clusterID,
			PlacementIds: []uuid.UUID{unknown},
			Revisions:    []int64{1},
		}
		rows, err := f.queries.RecordUnknownPlacements(t.Context(), params)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("cluster %s recorded %d unknown placements, want 1", clusterID, len(rows))
		}
	}
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM clusters WHERE id = $1`, f.otherCluster); err != nil {
		t.Fatal(err)
	}
	var remaining int
	row := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM cluster_unknown_placements`)
	if err := row.Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("cluster cleanup left %d records, want the other cluster's record only", remaining)
	}
}
