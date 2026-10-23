package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/migrations"
	"github.com/team-loco/loco/internal/clusterassertion"
)

type deployFixture struct {
	pool         *pgxpool.Pool
	queries      *genDb.Queries
	clusterID    uuid.UUID
	otherCluster uuid.UUID
	resourceID   uuid.UUID
	envID        uuid.UUID
}

const testAssertionLifetime = time.Minute

func testClusterKey(clusterID uuid.UUID) ed25519.PrivateKey {
	seed := sha256.Sum256(clusterID[:])
	return ed25519.NewKeyFromSeed(seed[:])
}

func testAssertion(t *testing.T, clusterID uuid.UUID) string {
	t.Helper()
	token, err := clusterassertion.Sign(
		testClusterKey(clusterID),
		clusterID.String(),
		time.Now(),
		testAssertionLifetime,
	)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func agentContext(ctx context.Context, t *testing.T, clusterID uuid.UUID) context.Context {
	t.Helper()
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+testAssertion(t, clusterID))
	return ctx
}

func enrollTestCluster(t *testing.T, pool *pgxpool.Pool, clusterID uuid.UUID) {
	t.Helper()
	publicKey, ok := testClusterKey(clusterID).Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("ed25519 private key returned a non-ed25519 public key")
	}
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE clusters SET cluster_key = id, agent_public_key = $2 WHERE id = $1",
		clusterID,
		[]byte(publicKey),
	); err != nil {
		t.Fatal(err)
	}
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

	f := &deployFixture{pool: pool, queries: genDb.New(pool)}
	row := pool.QueryRow(ctx, `
WITH u AS (
    INSERT INTO users (email) VALUES ('test@example.com') RETURNING id
), o AS (
    INSERT INTO organizations (name, created_by) SELECT 'org', id FROM u RETURNING id
), w AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'ws', u.id FROM o, u RETURNING id
), e AS (
    INSERT INTO environments (workspace_id, name, created_by) SELECT w.id, 'prod', u.id FROM w, u RETURNING id
), c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c1', 'us-east-1', 'kind', true, true) RETURNING id
), c2 AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c2', 'us-east-1', 'kind', true, false) RETURNING id
), r AS (
    INSERT INTO resources (workspace_id, name, type, description, spec, spec_version)
    SELECT w.id, 'svc', 'service', '', '{}', 1 FROM w RETURNING id
)
SELECT c.id, c2.id, r.id, e.id FROM c, c2, r, e`)
	if err := row.Scan(&f.clusterID, &f.otherCluster, &f.resourceID, &f.envID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	enrollTestCluster(t, pool, f.clusterID)
	enrollTestCluster(t, pool, f.otherCluster)
	return f
}

func (f *deployFixture) paramsFor(clusterID uuid.UUID) genDb.CreateDeploymentParams {
	return genDb.CreateDeploymentParams{
		ResourceID:    f.resourceID,
		ClusterID:     clusterID,
		Region:        "us-east-1",
		Replicas:      1,
		Status:        genDb.DeploymentStatusPending,
		IsActive:      true,
		Spec:          []byte("{}"),
		SpecVersion:   1,
		EnvironmentID: f.envID,
		SecretNames:   []string{},
	}
}

func staticSpec(_ uuid.UUID) ([]byte, error) {
	return []byte(`{"resource_id":"r"}`), nil
}

func (f *deployFixture) deploy(ctx context.Context, buildSpec desiredSpecFunc) (uuid.UUID, error) {
	return f.deployTo(ctx, f.clusterID, buildSpec)
}

func (f *deployFixture) deployTo(
	ctx context.Context,
	clusterID uuid.UUID,
	buildSpec desiredSpecFunc,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		var deployErr error
		id, deployErr = createDeploymentWithCleanup(ctx, qtx, f.paramsFor(clusterID), buildSpec)
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

func (f *deployFixture) placement(t *testing.T, clusterID uuid.UUID) genDb.Placement {
	t.Helper()
	p, err := f.queries.GetPlacementForResourceCluster(context.Background(), genDb.GetPlacementForResourceClusterParams{
		ResourceID: f.resourceID,
		ClusterID:  clusterID,
	})
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	return p
}

func (f *deployFixture) deploymentStatus(t *testing.T, id uuid.UUID) genDb.DeploymentStatus {
	t.Helper()
	d, err := f.queries.GetDeploymentByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return d.Status
}

func TestConcurrentDeploysLeaveOneActiveDeploymentAndPlacement(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	const deploys = 8
	var wg sync.WaitGroup
	errs := make(chan error, deploys)
	for range deploys {
		wg.Go(func() {
			if _, err := f.deploy(ctx, staticSpec); err != nil {
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
	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d placements, want 1", n)
	}
	p := f.placement(t, f.clusterID)
	if p.DesiredRevision != deploys {
		t.Fatalf("desired revision = %d, want %d", p.DesiredRevision, deploys)
	}
	var active uuid.UUID
	err := f.pool.QueryRow(ctx, `SELECT id FROM deployments WHERE resource_id = $1 AND is_active`, f.resourceID).
		Scan(&active)
	if err != nil {
		t.Fatalf("active deployment: %v", err)
	}
	if p.DeploymentID == nil || *p.DeploymentID != active {
		t.Fatalf("placement deployment = %v, want the active deployment %v", p.DeploymentID, active)
	}
}

func TestDeployRollsBackWhenSpecFails(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	firstID, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("first deploy: %v", err)
	}

	boom := errors.New("boom")
	_, err = f.deploy(ctx, func(uuid.UUID) ([]byte, error) { return nil, boom })
	if !errors.Is(err, errDesiredSpec) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want a desired spec error wrapping boom", err)
	}

	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after a failed deploy, want 1", n)
	}
	p := f.placement(t, f.clusterID)
	if p.DesiredRevision != 1 || p.DeploymentID == nil || *p.DeploymentID != firstID {
		t.Fatalf(
			"placement = rev %d deployment %v, want rev 1 deployment %v",
			p.DesiredRevision,
			p.DeploymentID,
			firstID,
		)
	}
}

func TestDeployToAnotherClusterMarksOldPlacementDeleted(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if _, err := f.deployTo(ctx, f.otherCluster, staticSpec); err != nil {
		t.Fatalf("deploy to other cluster: %v", err)
	}

	old := f.placement(t, f.clusterID)
	if !old.DesiredDeleted || old.DesiredSpec != nil || old.DesiredRevision != 2 {
		t.Fatalf("old placement = deleted %v spec %q rev %d, want deleted, no spec, rev 2",
			old.DesiredDeleted, old.DesiredSpec, old.DesiredRevision)
	}
	moved := f.placement(t, f.otherCluster)
	if moved.DesiredDeleted || moved.DesiredRevision != 1 {
		t.Fatalf("new placement = deleted %v rev %d, want live rev 1", moved.DesiredDeleted, moved.DesiredRevision)
	}
}
