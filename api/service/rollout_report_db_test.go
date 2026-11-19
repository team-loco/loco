package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (f *deployFixture) rolloutContext(t *testing.T) context.Context {
	t.Helper()
	var userID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	return reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			ActorType: genDb.DeploymentActorTypeUser,
			Method:    genDb.DeploymentMethodWeb,
			Entity:    entity,
			Scopes:    scopes,
		},
	)
}

func (f *deployFixture) reportRollout(t *testing.T, expired, ready bool) {
	t.Helper()
	p := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:              p.ID.String(),
		ObservedRevision:         p.DesiredRevision,
		Available:                ready,
		ProgressDeadlineExceeded: expired,
		ReadyReplicas:            1,
	})
}

func (f *deployFixture) finishRollouts(t *testing.T) {
	t.Helper()
	rows, err := f.pool.Query(
		t.Context(),
		`SELECT id, cluster_id, desired_revision FROM placements WHERE NOT desired_deleted`,
	)
	if err != nil {
		t.Fatal(err)
	}
	type report struct {
		placementID, clusterID uuid.UUID
		revision               int64
	}
	var reports []report
	for rows.Next() {
		var r report
		if scanErr := rows.Scan(&r.placementID, &r.clusterID, &r.revision); scanErr != nil {
			rows.Close()
			t.Fatal(scanErr)
		}
		reports = append(reports, r)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	for _, r := range reports {
		f.agentServer().
			recordStatus(t.Context(), r.clusterID, 0, &agentv1.PlacementStatus{
				PlacementId:      r.placementID.String(),
				ObservedRevision: r.revision,
				Available:        true,
				ReadyReplicas:    1,
			})
	}
}

func (f *deployFixture) rolloutSnapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	query := `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM deployments d),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM placements p),
 (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.name) FROM secrets s),
 (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM environments e))::text`
	if err := f.pool.QueryRow(t.Context(), query).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
