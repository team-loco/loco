package sweepers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/buildsource"
)

const testRegistryHost = "registry.loco.test"

type fixture struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	queries      *genDb.Queries
	workspaceID  uuid.UUID
	resourceID   uuid.UUID
	clusterID    uuid.UUID
	otherCluster uuid.UUID
	envID        uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	f := &fixture{ctx: context.Background(), pool: pool, queries: genDb.New(pool)}
	row := pool.QueryRow(f.ctx, `
WITH u AS (
    INSERT INTO users (email) VALUES ('test@example.com') RETURNING id
), o AS (
    INSERT INTO organizations (name, created_by) SELECT 'org', id FROM u RETURNING id
), w AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'ws', u.id FROM o, u RETURNING id
), e AS (
    INSERT INTO environments (workspace_id, name, created_by) SELECT w.id, 'prod', u.id FROM w, u RETURNING id
), c AS (
    INSERT INTO clusters (applications_allowed, builds_allowed, name, region, provider, is_active, is_default)
    VALUES (true, true, 'c1', 'us-east-1', 'kind', true, true) RETURNING id
), c2 AS (
    INSERT INTO clusters (applications_allowed, builds_allowed, name, region, provider, is_active, is_default)
    VALUES (true, true, 'c2', 'us-east-1', 'kind', true, false) RETURNING id
), r AS (
    INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
    SELECT w.id, 'svc', 'svc', 'service', '', '{}', 1 FROM w RETURNING id
)
SELECT w.id, c.id, c2.id, r.id, e.id FROM w, c, c2, r, e`)
	if err := row.Scan(&f.workspaceID, &f.clusterID, &f.otherCluster, &f.resourceID, &f.envID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

// insertBuild adds a build of the main resource; finished builds get a finish time of now.
func (f *fixture) insertBuild(t *testing.T, status genDb.BuildStatus) uuid.UUID {
	t.Helper()
	var finished *time.Time
	switch status {
	case genDb.BuildStatusSucceeded, genDb.BuildStatusFailed, genDb.BuildStatusCanceled:
		now := time.Now()
		finished = &now
	}
	return f.insertBuildFinishedAt(t, status, finished)
}

func (f *fixture) insertBuildFinishedAt(t *testing.T, status genDb.BuildStatus, finished *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO builds (id, resource_id, cluster_id, status, source_type, source_key, source_size,
                    dockerfile_path, image_repository, created_by, finished_at)
VALUES ($1, $2, $3, $4, 'upload', $5, 10, 'Dockerfile', $6, $7, $8)`,
		id, f.resourceID, f.clusterID, status, buildsource.Key(id), testRegistryHost+"/x", uuid.New(), finished,
	); err != nil {
		t.Fatalf("insert build: %v", err)
	}
	return id
}

func (f *fixture) build(t *testing.T, id uuid.UUID) genDb.Build {
	t.Helper()
	build, err := f.queries.GetBuildByID(f.ctx, id)
	if err != nil {
		t.Fatalf("get build %s: %v", id, err)
	}
	return build
}

func (f *fixture) otherResource(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `
INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
SELECT workspace_id, n.name, n.name, type, '', '{}', 1
FROM resources, (SELECT 'other-' || substr(md5(random()::text), 1, 8) AS name) n WHERE id = $1
RETURNING id`, f.resourceID).Scan(&id)
	if err != nil {
		t.Fatalf("create other resource: %v", err)
	}
	return id
}
