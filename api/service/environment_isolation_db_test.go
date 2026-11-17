package service

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/configplan"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	stagingName   = "staging"
	stagingDomain = "svc-staging.example.com"
)

func (f *deployFixture) addStagingEnvironment(t *testing.T) uuid.UUID {
	t.Helper()
	return f.addEnvironment(t, stagingName)
}

func (f *deployFixture) activeDeployments(t *testing.T, environmentID uuid.UUID) []genDb.Deployment {
	t.Helper()
	deployments, err := f.queries.ListActiveDeploymentsForEnvironment(context.Background(), environmentID)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	return deployments
}

func TestDeploymentsOfTwoEnvironmentsInOneRegionStayActive(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)

	ctx := context.Background()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	staging := f.paramsFor(f.otherCluster)
	staging.EnvironmentID = stagingID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(ctx, qtx, limits.Install(limits.Defaults()), staging, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy staging: %v", err)
	}
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("redeploy production: %v", err)
	}

	if got := len(f.activeDeployments(t, f.envID)); got != 1 {
		t.Fatalf("production active deployments = %d, want 1", got)
	}
	if got := len(f.activeDeployments(t, stagingID)); got != 1 {
		t.Fatalf("staging active deployments = %d, want 1 after production redeployed", got)
	}
}

func TestPlanSeesOnlyTheDomainsOfItsEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	f.setRoutedSpec(t)
	f.addRunningOwned(t)
	stagingID := f.addStagingEnvironment(t)
	insert := `
INSERT INTO resource_domains (resource_id, environment_id, domain, domain_source, is_primary)
SELECT id, $2, $3, 'user_provided', true FROM resources WHERE name = $1`
	if _, err := f.pool.Exec(context.Background(), insert, planOwned, stagingID, stagingDomain); err != nil {
		t.Fatalf("add staging domain: %v", err)
	}

	file := planFileHeader + planFileOwnedAsLive()
	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 0 {
		t.Fatalf("production plan = %v, want nothing for a domain staging holds", resp)
	}
}

func TestPlanAcceptsOnlyRegionsWithAHealthyCluster(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no healthy cluster":   `UPDATE clusters SET health_status = 'unhealthy', last_heartbeat = NOW()`,
		"health never checked": `UPDATE clusters SET health_status = NULL, last_heartbeat = NOW()`,
		"heartbeat stale": `UPDATE clusters SET health_status = 'healthy',
			last_heartbeat = NOW() - INTERVAL '1 hour'`,
	}
	for name, update := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeployFixture(t)
			if _, err := f.pool.Exec(context.Background(), update); err != nil {
				t.Fatalf("update clusters: %v", err)
			}
			resp, err := plan(t, f, planFileHeader+planFileNew, f.workspaceReadScopes(t))
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			errs := resp.GetErrors()
			if len(errs) != 1 || errs[0].GetPath() != "regions.us-east-1" ||
				errs[0].GetMessage() != configplan.ErrUnknownRegion.Error()+": us-east-1" {
				t.Fatalf("errors = %v, want the region refused", errs)
			}
		})
	}
}

func TestEnvironmentsShareAClusterWithIndependentDeploymentLifecycles(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.deployToProductionAndStaging(t)
	production := f.activeDeployment(t, f.envID)
	staging := f.activeDeployment(t, stagingID)
	if production.ClusterID != f.clusterID || staging.ClusterID != f.clusterID {
		t.Fatalf(
			"clusters = %s, %s, want both environments on %s",
			production.ClusterID,
			staging.ClusterID,
			f.clusterID,
		)
	}
	f.finishRollouts(t)
	productionPlacement := f.placementIn(t, f.envID, f.clusterID)
	stagingPlacement := f.placementIn(t, stagingID, f.clusterID)
	if productionPlacement.ID == stagingPlacement.ID {
		t.Fatal("environments share a placement identity")
	}
	if err := scaleResourceIn(t, f, f.envID, 2); err != nil {
		t.Fatal(err)
	}
	if other := f.placementIn(t, stagingID, f.clusterID); other.DesiredRevision != stagingPlacement.DesiredRevision ||
		!bytes.Equal(other.DesiredSpec, stagingPlacement.DesiredSpec) {
		t.Fatal("scaling production changed the staging placement")
	}
	f.finishRollouts(t)
	if err := scaleResourceIn(t, f, stagingID, 3); err != nil {
		t.Fatal(err)
	}
	if got := f.activeDeployment(t, f.envID); got.Replicas != 2 {
		t.Fatalf("production replicas = %d after staging scale, want 2", got.Replicas)
	}
	staging = f.activeDeployment(t, stagingID)
	f.finishRollouts(t)
	if _, err := deployServiceIn(t, f, imageService(), f.envID); err != nil {
		t.Fatal(err)
	}
	if got := f.activeDeployment(t, stagingID); got.ID != staging.ID || got.Replicas != 3 {
		t.Fatalf("staging deployment after production redeploy = %+v", got)
	}
	f.finishRollouts(t)
	current := f.activeDeployment(t, f.envID)
	server, ctx := f.deploymentWriter(t)
	if _, err := server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: current.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if got := f.placementIn(t, f.envID, f.clusterID); !got.DesiredDeleted {
		t.Fatal("production placement remains live after deletion")
	}
	if got := f.placementIn(t, stagingID, f.clusterID); got.DesiredDeleted || got.ID != stagingPlacement.ID {
		t.Fatal("production deletion removed or replaced the staging placement")
	}
	if got := f.activeDeployment(t, stagingID); got.ID != staging.ID || got.Replicas != 3 {
		t.Fatalf("staging deployment after production deletion = %+v", got)
	}
	var open int
	countWindows := func() int {
		t.Helper()
		query := `SELECT count(*) FROM placement_windows
WHERE resource_id=$1 AND cluster_id=$2 AND removed_at IS NULL`
		if err := f.pool.QueryRow(t.Context(), query, f.resourceID, f.clusterID).Scan(&open); err != nil {
			t.Fatal(err)
		}
		return open
	}
	if got := countWindows(); got != 1 {
		t.Fatalf("open windows after production deletion = %d, want one for staging", got)
	}
	if _, err := server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: staging.ID.String()},
	); err != nil {
		t.Fatal(err)
	}
	if got := countWindows(); got != 0 {
		t.Fatalf("open windows after last environment deletion = %d, want none", got)
	}
}

// Build completion and rollback hold the resource row, without the environment
// locks an interactive request takes. Deletion must join that same lock protocol.
func TestDeletionWaitsForAnotherEnvironmentsPlacement(t *testing.T) {
	for _, resource := range []bool{false, true} {
		t.Run(map[bool]string{false: "deployment", true: "entire resource"}[resource], func(t *testing.T) {
			f := newDeployFixture(t)
			ctx := t.Context()
			production, err := f.deploy(ctx, staticSpec)
			if err != nil {
				t.Fatal(err)
			}
			staging := f.addStagingEnvironment(t)
			tx, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var writers sync.WaitGroup
			t.Cleanup(func() {
				if rollbackErr := tx.Rollback(
					context.Background(),
				); rollbackErr != nil &&
					!errors.Is(rollbackErr, pgx.ErrTxClosed) {
					t.Error(rollbackErr)
				}
				writers.Wait()
			})
			qtx := genDb.New(tx)
			if err = lockResource(ctx, qtx, f.resourceID); err != nil {
				t.Fatal(err)
			}
			if err = placeApplication(ctx, qtx, genDb.UpsertPlacementParams{
				ResourceID: f.resourceID, EnvironmentID: staging, ClusterID: f.clusterID,
				Region: testRegion, DesiredSpec: []byte(`{"app_spec":{}}`), SecretNames: []string{},
			}); err != nil {
				t.Fatal(err)
			}
			// The uncommitted staging placement reuses production's open window.
			// Closing that window without the resource lock cannot see staging yet.
			deploymentServer, scoped := f.deploymentWriter(t)
			resourceServer := NewResourceServer(
				f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults(),
			)
			admin := reqctx.WithPrincipal(ctx, reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})
			var deleteErr error
			writers.Go(func() {
				if resource {
					_, deleteErr = resourceServer.DeleteResource(admin, &resourcev1.DeleteResourceRequest{
						ResourceId: f.resourceID.String(),
					})
				} else {
					_, deleteErr = deploymentServer.DeleteDeployment(scoped, &deploymentv1.DeleteDeploymentRequest{
						DeploymentId: production.String(),
					})
				}
			})
			dbtest.WaitForLockWaiters(t, f.pool, 1)
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			writers.Wait()
			if deleteErr != nil {
				t.Fatal(deleteErr)
			}
			var live, open int
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM placements
WHERE resource_id=$1 AND cluster_id=$2 AND NOT desired_deleted`, f.resourceID, f.clusterID).Scan(&live); err != nil {
				t.Fatal(err)
			}
			if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM placement_windows
WHERE resource_id=$1 AND cluster_id=$2 AND removed_at IS NULL`, f.resourceID, f.clusterID).Scan(&open); err != nil {
				t.Fatal(err)
			}
			want := 1
			if resource {
				want = 0
			}
			if live != want || open != want {
				t.Fatalf("live placements=%d, open windows=%d, want both %d", live, open, want)
			}
		})
	}
}
