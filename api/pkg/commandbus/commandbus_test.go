package commandbus_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/migrations"
	"github.com/team-loco/loco/api/pkg/commandbus"
)

func TestBackoffDoublesUpToCap(t *testing.T) {
	cases := []struct {
		attempts int32
		want     time.Duration
	}{
		{attempts: 0, want: 5 * time.Second},
		{attempts: 1, want: 5 * time.Second},
		{attempts: 2, want: 10 * time.Second},
		{attempts: 3, want: 20 * time.Second},
		{attempts: 7, want: 5 * time.Minute},
		{attempts: 64, want: 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := commandbus.Backoff(tc.attempts); got != tc.want {
			t.Errorf("Backoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestChannelNameFitsPostgresIdentifierLimit(t *testing.T) {
	name := commandbus.ChannelName(uuid.New())
	if len(name) > 63 {
		t.Fatalf("channel name %q is %d bytes, postgres truncates identifiers past 63", name, len(name))
	}
}

type fixture struct {
	pool         *pgxpool.Pool
	queries      *genDb.Queries
	clusterID    uuid.UUID
	otherCluster uuid.UUID
	resourceID   uuid.UUID
	regionID     uuid.UUID
	envID        uuid.UUID
}

func newFixture(t *testing.T) *fixture {
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

	dbName := "commandbus_" + uuid.NewString()[:8]
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

	f := &fixture{pool: pool, queries: genDb.New(pool)}
	row := pool.QueryRow(ctx, `
WITH u AS (
    INSERT INTO users (external_id, email) VALUES ('github:test', 'test@example.com') RETURNING id
), o AS (
    INSERT INTO organizations (name, created_by) SELECT 'org', id FROM u RETURNING id
), w AS (
    INSERT INTO workspaces (org_id, name, created_by) SELECT o.id, 'ws', u.id FROM o, u RETURNING id
), e AS (
    INSERT INTO environments (workspace_id, name, created_by) SELECT w.id, 'prod', u.id FROM w, u RETURNING id
), c1 AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c1', 'us-east-1', 'kind', true, true) RETURNING id
), c2 AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c2', 'eu-west-1', 'kind', true, false) RETURNING id
), r AS (
    INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
    SELECT w.id, 'svc', 'service', '', 'healthy', '{}', 1 FROM w RETURNING id
), rr AS (
    INSERT INTO resource_regions (resource_id, region, is_primary, status)
    SELECT r.id, 'us-east-1', true, 'active' FROM r RETURNING id
)
SELECT c1.id, c2.id, r.id, rr.id, e.id FROM c1, c2, r, rr, e`)
	if err := row.Scan(&f.clusterID, &f.otherCluster, &f.resourceID, &f.regionID, &f.envID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

func (f *fixture) createDeployment(t *testing.T, active bool) uuid.UUID {
	t.Helper()
	id, err := f.queries.CreateDeployment(context.Background(), genDb.CreateDeploymentParams{
		ResourceID:       f.resourceID,
		ResourceRegionID: f.regionID,
		ClusterID:        f.clusterID,
		Region:           "us-east-1",
		Replicas:         1,
		Status:           genDb.DeploymentStatusPending,
		IsActive:         active,
		Message:          "",
		Spec:             []byte("{}"),
		SpecVersion:      1,
		EnvironmentID:    f.envID,
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return id
}

func (f *fixture) enqueue(t *testing.T, resourceID uuid.UUID, deploymentID *uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		var enqueueErr error
		id, enqueueErr = commandbus.Enqueue(ctx, qtx, commandbus.NewCommand{
			ClusterID:    f.clusterID,
			ResourceID:   resourceID,
			DeploymentID: deploymentID,
			Type:         commandbus.CommandTypeDeploy,
			Payload:      []byte(`{"secret":"value"}`),
		})
		return enqueueErr
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return id
}

func (f *fixture) command(t *testing.T, id uuid.UUID) genDb.AgentCommand {
	t.Helper()
	var c genDb.AgentCommand
	err := f.pool.QueryRow(context.Background(),
		`SELECT status, attempts, payload, last_error, visible_at FROM agent_commands WHERE id = $1`, id,
	).Scan(&c.Status, &c.Attempts, &c.Payload, &c.LastError, &c.VisibleAt)
	if err != nil {
		t.Fatalf("load command: %v", err)
	}
	return c
}

func claimIDs(t *testing.T, bus *commandbus.Bus, clusterID uuid.UUID) []uuid.UUID {
	t.Helper()
	cmds, err := bus.Claim(context.Background(), clusterID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(cmds))
	for _, c := range cmds {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestClaimLeasesOldestFirstAndHidesLeased(t *testing.T) {
	f := newFixture(t)
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{BatchSize: 2})

	first := f.enqueue(t, uuid.New(), nil)
	second := f.enqueue(t, uuid.New(), nil)
	third := f.enqueue(t, uuid.New(), nil)

	got := claimIDs(t, bus, f.clusterID)
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("first claim = %v, want [%v %v]", got, first, second)
	}
	got = claimIDs(t, bus, f.clusterID)
	if len(got) != 1 || got[0] != third {
		t.Fatalf("second claim = %v, want [%v]", got, third)
	}
	if got := claimIDs(t, bus, f.clusterID); len(got) != 0 {
		t.Fatalf("leased commands were claimed again: %v", got)
	}
	if got := claimIDs(t, bus, f.otherCluster); len(got) != 0 {
		t.Fatalf("another cluster claimed %v", got)
	}
	if c := f.command(t, first); c.Status != genDb.AgentCommandStatusDelivered || c.Attempts != 1 {
		t.Fatalf("claimed command status=%s attempts=%d", c.Status, c.Attempts)
	}
}

func TestEnqueueSupersedesOlderCommandsForResource(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{})

	oldID := f.enqueue(t, f.resourceID, nil)
	if got := claimIDs(t, bus, f.clusterID); len(got) != 1 || got[0] != oldID {
		t.Fatalf("claim = %v, want [%v]", got, oldID)
	}
	unrelated := f.enqueue(t, uuid.New(), nil)
	newID := f.enqueue(t, f.resourceID, nil)

	old := f.command(t, oldID)
	if old.Status != genDb.AgentCommandStatusSuperseded || old.Payload != nil {
		t.Fatalf("old command status=%s payload=%s, want superseded with no payload", old.Status, old.Payload)
	}
	if err := bus.Ack(ctx, f.clusterID, oldID); err != nil {
		t.Fatalf("ack superseded: %v", err)
	}
	if c := f.command(t, oldID); c.Status != genDb.AgentCommandStatusSuperseded {
		t.Fatalf("ack revived superseded command: %s", c.Status)
	}
	if err := bus.Nack(ctx, f.clusterID, oldID, true, "boom"); err != nil {
		t.Fatalf("nack superseded: %v", err)
	}
	if c := f.command(t, oldID); c.Status != genDb.AgentCommandStatusSuperseded {
		t.Fatalf("nack revived superseded command: %s", c.Status)
	}

	got := claimIDs(t, bus, f.clusterID)
	if len(got) != 2 || got[0] != unrelated || got[1] != newID {
		t.Fatalf("claim = %v, want [%v %v]", got, unrelated, newID)
	}
}

func TestAckIsScopedToCluster(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{})

	id := f.enqueue(t, f.resourceID, nil)
	claimIDs(t, bus, f.clusterID)

	if err := bus.Ack(ctx, f.otherCluster, id); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if c := f.command(t, id); c.Status != genDb.AgentCommandStatusDelivered {
		t.Fatalf("another cluster acked the command: %s", c.Status)
	}
	if err := bus.Nack(ctx, f.otherCluster, id, false, "boom"); err != nil {
		t.Fatalf("nack: %v", err)
	}
	if c := f.command(t, id); c.Status != genDb.AgentCommandStatusDelivered {
		t.Fatalf("another cluster nacked the command: %s", c.Status)
	}

	if err := bus.Ack(ctx, f.clusterID, id); err != nil {
		t.Fatalf("ack: %v", err)
	}
	c := f.command(t, id)
	if c.Status != genDb.AgentCommandStatusSucceeded || c.Payload != nil {
		t.Fatalf("status=%s payload=%s, want succeeded with no payload", c.Status, c.Payload)
	}
}

func TestNackRetriesWithBackoffThenFailsDeployment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{})

	deploymentID := f.createDeployment(t, true)
	id := f.enqueue(t, f.resourceID, &deploymentID)
	claimIDs(t, bus, f.clusterID)

	if err := bus.Nack(ctx, f.clusterID, id, true, "image pull failed"); err != nil {
		t.Fatalf("nack: %v", err)
	}
	c := f.command(t, id)
	if c.Status != genDb.AgentCommandStatusPending {
		t.Fatalf("status = %s, want pending", c.Status)
	}
	if wait := time.Until(c.VisibleAt); wait < 3*time.Second {
		t.Fatalf("retry visible in %v, want a backoff", wait)
	}
	if got := claimIDs(t, bus, f.clusterID); len(got) != 0 {
		t.Fatalf("command claimed during backoff: %v", got)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE agent_commands SET visible_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatalf("expire backoff: %v", err)
	}
	cmds, err := bus.Claim(ctx, f.clusterID)
	if err != nil || len(cmds) != 1 || cmds[0].Attempts != 2 {
		t.Fatalf("redelivery = %+v, %v; want one command on attempt 2", cmds, err)
	}

	if nackErr := bus.Nack(ctx, f.clusterID, id, false, "invalid spec"); nackErr != nil {
		t.Fatalf("nack: %v", nackErr)
	}
	c = f.command(t, id)
	if c.Status != genDb.AgentCommandStatusFailed || c.LastError == nil || *c.LastError != "invalid spec" {
		t.Fatalf("status=%s last_error=%v, want failed with the agent's error", c.Status, c.LastError)
	}
	if c.Payload != nil {
		t.Fatalf("failed command kept its payload")
	}

	d, err := f.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.Status != genDb.DeploymentStatusFailed || d.Message != "invalid spec" || d.CompletedAt == nil {
		t.Fatalf("deployment status=%s message=%q completed_at=%v", d.Status, d.Message, d.CompletedAt)
	}
}

func TestExpiredLeaseIsRedeliveredThenFailsWhenAttemptsRunOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{Lease: 100 * time.Millisecond})

	deploymentID := f.createDeployment(t, true)
	id := f.enqueue(t, f.resourceID, &deploymentID)
	if _, err := f.pool.Exec(ctx, `UPDATE agent_commands SET max_attempts = 2 WHERE id = $1`, id); err != nil {
		t.Fatalf("set max attempts: %v", err)
	}

	if got := claimIDs(t, bus, f.clusterID); len(got) != 1 {
		t.Fatalf("first delivery = %v", got)
	}
	time.Sleep(200 * time.Millisecond)
	cmds, err := bus.Claim(ctx, f.clusterID)
	if err != nil || len(cmds) != 1 || cmds[0].Attempts != 2 {
		t.Fatalf("redelivery = %+v, %v; want one command on attempt 2", cmds, err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := claimIDs(t, bus, f.clusterID); len(got) != 0 {
		t.Fatalf("command delivered past max attempts: %v", got)
	}

	if c := f.command(t, id); c.Status != genDb.AgentCommandStatusFailed {
		t.Fatalf("status = %s, want failed", c.Status)
	}
	d, err := f.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.Status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", d.Status)
	}
}

func TestListenerWakesOnCommittedEnqueue(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{PollInterval: time.Hour})

	listener, err := bus.Listen(ctx, f.clusterID)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	rollback := errors.New("rollback")
	err = pgx.BeginFunc(ctx, f.pool, func(tx pgx.Tx) error {
		qtx := genDb.New(tx)
		if _, enqueueErr := commandbus.Enqueue(ctx, qtx, commandbus.NewCommand{
			ClusterID:  f.clusterID,
			ResourceID: f.resourceID,
			Type:       commandbus.CommandTypeDelete,
			Payload:    []byte(`{}`),
		}); enqueueErr != nil {
			return enqueueErr
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("enqueue in rolled back tx: %v", err)
	}
	select {
	case <-listener.Wake():
		t.Fatal("woken by a rolled back enqueue")
	case <-time.After(300 * time.Millisecond):
	}

	f.enqueue(t, f.resourceID, nil)
	select {
	case <-listener.Wake():
	case err := <-listener.Err():
		t.Fatalf("listener failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener not woken by a committed enqueue")
	}
}

func TestListenerReleasesItsConnection(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bus := commandbus.New(f.pool, f.queries, commandbus.Config{})

	for i := range 3 {
		listener, err := bus.Listen(ctx, f.clusterID)
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listener.Close()
	}
	if acquired := f.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still acquired after closing listeners", acquired)
	}
	var listening int
	err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_listening_channels()`).Scan(&listening)
	if err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if listening != 0 {
		t.Fatalf("pooled connection still listening on %d channels", listening)
	}
}

func TestOnlyOneActiveDeploymentPerResourceRegion(t *testing.T) {
	f := newFixture(t)
	f.createDeployment(t, true)
	f.createDeployment(t, false)
	_, err := f.queries.CreateDeployment(context.Background(), genDb.CreateDeploymentParams{
		ResourceID:       f.resourceID,
		ResourceRegionID: f.regionID,
		ClusterID:        f.clusterID,
		Region:           "us-east-1",
		Replicas:         1,
		Status:           genDb.DeploymentStatusPending,
		IsActive:         true,
		Spec:             []byte("{}"),
		SpecVersion:      1,
		EnvironmentID:    f.envID,
	})
	if err == nil {
		t.Fatalf("second active deployment for %s/us-east-1 was accepted", f.resourceID)
	}
}
