package service

import (
	"context"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/migrations"
)

type deployFixture struct {
	pool       *pgxpool.Pool
	clusterID  uuid.UUID
	resourceID uuid.UUID
	envID      uuid.UUID
}

func newDeployFixture(t *testing.T) *deployFixture {
	t.Helper()
	baseURL := os.Getenv("LOCO_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("LOCO_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := admin.Close(context.Background()); closeErr != nil {
			t.Logf("close admin connection: %v", closeErr)
		}
	})

	dbName := "service_" + uuid.NewString()[:8]
	ident := pgx.Identifier{dbName}.Sanitize()
	if _, createErr := admin.Exec(ctx, "CREATE DATABASE "+ident); createErr != nil {
		t.Fatalf("create database: %v", createErr)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	parsed.Path = "/" + dbName
	dbURL := parsed.String()

	if migrateErr := migrations.Up(ctx, dbURL); migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, dropErr := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); dropErr != nil {
			t.Logf("drop database: %v", dropErr)
		}
	})

	f := &deployFixture{pool: pool}
	row := pool.QueryRow(ctx, `
WITH u AS (
    INSERT INTO users (external_id, email) VALUES ('github:test', 'test@example.com') RETURNING id
), o AS (
    INSERT INTO organizations (name, created_by) SELECT 'org', id FROM u RETURNING id
), w AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'ws', u.id FROM o, u RETURNING id
), e AS (
    INSERT INTO environments (workspace_id, name, created_by) SELECT w.id, 'prod', u.id FROM w, u RETURNING id
), c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c1', 'us-east-1', 'kind', true, true) RETURNING id
), r AS (
    INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
    SELECT w.id, 'svc', 'service', '', 'healthy', '{}', 1 FROM w RETURNING id
), rr AS (
    INSERT INTO resource_regions (resource_id, region, is_primary, status)
    SELECT r.id, 'us-east-1', true, 'active' FROM r RETURNING id
)
SELECT c.id, r.id, e.id FROM c, r, e, rr`)
	if err := row.Scan(&f.clusterID, &f.resourceID, &f.envID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

func (f *deployFixture) params() genDb.CreateDeploymentParams {
	return genDb.CreateDeploymentParams{
		ResourceID:    f.resourceID,
		ClusterID:     f.clusterID,
		Region:        "us-east-1",
		Replicas:      1,
		Status:        genDb.DeploymentStatusPending,
		IsActive:      true,
		Spec:          []byte("{}"),
		SpecVersion:   1,
		EnvironmentID: f.envID,
	}
}

func staticPayload(_ uuid.UUID) ([]byte, error) {
	return []byte(`{}`), nil
}

func (f *deployFixture) deploy(ctx context.Context, buildPayload deployPayloadFunc) (uuid.UUID, error) {
	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = createDeploymentWithCleanup(ctx, qtx, f.params(), buildPayload)
		return deployErr
	})
	return id, err
}

func (f *deployFixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), query, f.resourceID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestConcurrentDeploysLeaveOneActiveDeploymentAndCommand(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	const deploys = 8
	var wg sync.WaitGroup
	errs := make(chan error, deploys)
	for range deploys {
		wg.Go(func() {
			if _, err := f.deploy(ctx, staticPayload); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("deploy: %v", err)
	}

	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1 AND is_active`); n != 1 {
		t.Fatalf("%d active deployments, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != deploys {
		t.Fatalf("%d deployments, want %d", n, deploys)
	}
	live := f.count(t, `SELECT count(*) FROM agent_commands
WHERE resource_id = $1 AND status IN ('pending', 'delivered')`)
	if live != 1 {
		t.Fatalf("%d live commands, want 1", live)
	}
	matched := f.count(t, `SELECT count(*) FROM agent_commands c
JOIN deployments d ON d.id = c.deployment_id
WHERE c.resource_id = $1 AND c.status = 'pending' AND d.is_active`)
	if matched != 1 {
		t.Fatal("the live command does not belong to the active deployment")
	}
}

func TestDeployRollsBackWhenPayloadFails(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	firstID, err := f.deploy(ctx, staticPayload)
	if err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	boom := errors.New("boom")
	_, err = f.deploy(ctx, func(uuid.UUID) ([]byte, error) { return nil, boom })
	if !errors.Is(err, errCommandPayload) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want a payload error wrapping boom", err)
	}

	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after a failed deploy, want 1", n)
	}
	var active uuid.UUID
	err = f.pool.QueryRow(ctx, `SELECT id FROM deployments WHERE resource_id = $1 AND is_active`, f.resourceID).
		Scan(&active)
	if err != nil {
		t.Fatalf("active deployment: %v", err)
	}
	if active != firstID {
		t.Fatalf("active deployment = %v, want %v", active, firstID)
	}
	live := f.count(t, `SELECT count(*) FROM agent_commands
WHERE resource_id = $1 AND status = 'pending'`)
	if live != 1 {
		t.Fatalf("%d pending commands after a failed deploy, want 1", live)
	}
}
