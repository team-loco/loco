package sweepers

import (
	"crypto/sha256"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestRegistrySweepDeletesDeadCredentials(t *testing.T) {
	f := newFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertPull := func(expiresIn string) []byte {
		hash := sha256.Sum256([]byte(uuid.NewString()))
		exec(`
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, secret_hash, expires_at)
VALUES ('pull', $1, $2, $3, NOW() + $4::interval)`,
			f.clusterID, f.workspaceID, hash[:], expiresIn,
		)
		return hash[:]
	}
	buildHash := func(buildID uuid.UUID) []byte {
		hash := sha256.Sum256([]byte(uuid.NewString()))
		exec(`
INSERT INTO registry_credentials (kind, cluster_id, workspace_id, build_id, secret_hash)
VALUES ('build', $1, $2, $3, $4)`,
			f.clusterID, f.workspaceID, buildID, hash[:],
		)
		return hash[:]
	}
	livePull := insertPull("1 hour")
	insertPull("-1 second")
	liveBuild := buildHash(f.insertBuild(t, genDb.BuildStatusRunning))
	buildHash(f.insertBuild(t, genDb.BuildStatusFailed))
	moved := f.insertBuild(t, genDb.BuildStatusRunning)
	buildHash(moved)
	exec("UPDATE builds SET cluster_id = $2 WHERE id = $1", moved, f.otherCluster)

	sweeper := NewRegistrySweeper(f.pool, time.Minute)
	result, err := sweeper.Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ran || result.CredentialsDeleted != 3 {
		t.Fatalf("deleted %d registry credentials, want 3", result.CredentialsDeleted)
	}
	rows, err := f.pool.Query(t.Context(), "SELECT secret_hash FROM registry_credentials ORDER BY kind DESC")
	if err != nil {
		t.Fatal(err)
	}
	var kept [][]byte
	for rows.Next() {
		var hash []byte
		if scanErr := rows.Scan(&hash); scanErr != nil {
			t.Fatal(scanErr)
		}
		kept = append(kept, hash)
	}
	if !reflect.DeepEqual(kept, [][]byte{livePull, liveBuild}) {
		t.Fatalf("kept %x, want the live pull and build credentials", kept)
	}
}
