package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/clusterassertion"
	"google.golang.org/protobuf/encoding/protojson"
)

type deployFixture struct {
	tenant       tenant
	pool         *pgxpool.Pool
	queries      *genDb.Queries
	clusterID    uuid.UUID
	otherCluster uuid.UUID
	resourceID   uuid.UUID
	envID        uuid.UUID
	userID       uuid.UUID
}

// deployFixtureEmail is the email of the user newDeployFixture creates.
const deployFixtureEmail = "test@example.com"

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
	pool := dbtest.NewPool(t)
	ctx := context.Background()

	f := &deployFixture{pool: pool, queries: genDb.New(pool)}
	tn := seedTenant(t, pool, f.queries, deployFixtureEmail, "org")
	f.tenant = tn
	workspaceID := tn.workspace(t, "ws")
	f.userID = tn.ownerID
	f.envID = tn.defaultEnvironment(t, workspaceID)
	tn.renameEnvironment(t, f.envID, statusProduction)
	f.resourceID = tn.resource(t, workspaceID, "svc", &resourcev1.ServiceSpec{
		Routing: &resourcev1.RoutingConfig{Port: new(int32(8080))},
	})
	row := pool.QueryRow(ctx, `
WITH c AS (
    INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
    VALUES (ARRAY['linux/amd64'], 'c1', 'us-east-1', 'kind', true, true) RETURNING id
), c2 AS (
    INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
    VALUES (ARRAY['linux/amd64'], 'c2', 'us-east-1', 'kind', true, false) RETURNING id
)
SELECT c.id, c2.id FROM c, c2`)
	if err := row.Scan(&f.clusterID, &f.otherCluster); err != nil {
		t.Fatalf("seed clusters: %v", err)
	}
	enrollTestCluster(t, pool, f.clusterID)
	enrollTestCluster(t, pool, f.otherCluster)
	return f
}

// testDeploymentParams is a deployment the tests record directly in one region.
type testDeploymentParams struct {
	genDb.CreateDeploymentParams
	ClusterID uuid.UUID
	Region    string
	Replicas  int32
}

func (f *deployFixture) paramsFor(clusterID uuid.UUID) testDeploymentParams {
	return testDeploymentParams{
		ActorType:     genDb.DeploymentActorTypeUser,
		Method:        genDb.DeploymentMethodCli,
		ResourceID:    f.resourceID,
		Spec:          []byte(`{"platform":"linux/amd64"}`),
		EnvironmentID: f.envID,
		SecretNames:   []string{},
		Trigger:       genDb.DeploymentTriggerDeploy,
		Status:        genDb.DeploymentStatusDeploying,
		ClusterID:     clusterID,
		Region:        testRegion,
		Replicas:      1,
	}
}

// recordTestDeployment records a deployment of params.Spec in params.Region with the payload
// buildSpec renders.
func recordTestDeployment(
	ctx context.Context,
	qtx *genDb.Queries,
	counts limits.Effective,
	params testDeploymentParams,
	buildSpec desiredSpecFunc,
) (uuid.UUID, error) {
	var service deploymentv1.ServiceDeploymentSpec
	if err := protojson.Unmarshal(params.Spec, &service); err != nil {
		return uuid.Nil, err
	}
	service.Regions = map[string]*deploymentv1.RegionDeploymentTarget{
		params.Region: {MinReplicas: &params.Replicas},
	}
	return createDeployment(ctx, qtx, &counts, newDeployment{
		params:  params.CreateDeploymentParams,
		service: &service,
		targets: []placementTarget{{region: params.Region, clusterID: params.ClusterID}},
		render: func(_ *deploymentv1.DeploymentSpec, deploymentID uuid.UUID, _ string) ([]byte, error) {
			return buildSpec(deploymentID)
		},
	})
}

func staticSpec(_ uuid.UUID) ([]byte, error) {
	return []byte(`{"app_spec":{}}`), nil
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
		counts, err := f.counts(ctx, qtx)
		if err != nil {
			return err
		}
		id, err = recordTestDeployment(ctx, qtx, counts, f.paramsFor(clusterID), buildSpec)
		return err
	})
	return id, err
}

// counts loads the limits of the fixture's organization, as a deploy does.
func (f *deployFixture) counts(ctx context.Context, qtx *genDb.Queries) (limits.Effective, error) {
	owner, err := qtx.GetWorkspaceOrganizationIDByResourceID(ctx, f.resourceID)
	if err != nil {
		return limits.Effective{}, err
	}
	return limits.ForOrg(ctx, qtx, limits.Defaults(), owner.OrgID)
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
	return f.placementIn(t, f.envID, clusterID)
}

func (f *deployFixture) placementIn(t *testing.T, environmentID, clusterID uuid.UUID) genDb.Placement {
	t.Helper()
	p, err := f.queries.GetResourceEnvironmentPlacement(
		context.Background(),
		genDb.GetResourceEnvironmentPlacementParams{EnvironmentID: environmentID,
			ResourceID: f.resourceID,
			ClusterID:  clusterID,
		},
	)
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
	t.Parallel()
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

	if n := f.count(t, `SELECT count(*) FROM live_deployment_placements WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d live deployment placements, want 1", n)
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
	err := f.pool.QueryRow(ctx, `SELECT deployment_id FROM live_deployment_placements WHERE resource_id = $1`,
		f.resourceID).Scan(&active)
	if err != nil {
		t.Fatalf("active deployment: %v", err)
	}
	if p.DeploymentID == nil || *p.DeploymentID != active {
		t.Fatalf("placement deployment = %v, want the active deployment %v", p.DeploymentID, active)
	}
}

func TestDeployRollsBackWhenSpecFails(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRunningOnAnotherClusterSupersedesTheRegionsPreviousDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := context.Background()
	report := func(clusterID, deploymentID uuid.UUID) {
		t.Helper()
		p := f.placement(t, clusterID)
		f.agentServer().recordStatus(ctx, clusterID, 0, &agentv1.PlacementStatus{
			PlacementId: p.ID.String(), DeploymentId: deploymentID.String(),
			ObservedRevision: p.DesiredRevision, Available: true, Healthy: true,
		})
	}
	first, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	report(f.clusterID, first)
	second, err := f.deployTo(ctx, f.otherCluster, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	report(f.otherCluster, second)
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusSuperseded {
		t.Fatalf("first = %s, want superseded once the region runs the second on another cluster", got)
	}
}
