package service

import (
	"bytes"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func restartResource(t *testing.T, f *deployFixture, environmentID uuid.UUID, region string, scope genDb.Scope) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: scope},
	}})
	var requestedRegion *string
	if region != "" {
		requestedRegion = &region
	}
	_, err := server.RestartResource(ctx, &resourcev1.RestartResourceRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: environmentID.String(), Region: requestedRegion,
	})
	return err
}

func TestRestartResourcePreservesSpecsAndTargetsOnlyTheRequestedPlacements(t *testing.T) {
	t.Parallel()
	for _, region := range []string{"", testRegion} {
		t.Run("region="+region, func(t *testing.T) {
			f := newDeployFixture(t)
			stagingID := f.deployToProductionAndStaging(t)
			f.addRegionCluster(t)
			f.finishRollouts(t)
			seedDeployment(t, f, domainsTwoRegionSpec)
			_, err := deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Regions:       []string{statusSecondRegion},
				Spec: &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			f.finishRollouts(t)
			var placements []genDb.Placement
			for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
				rows, err := f.queries.ListResourceEnvironmentPlacements(
					t.Context(),
					genDb.ListResourceEnvironmentPlacementsParams{
						ResourceID: f.resourceID, EnvironmentID: environmentID,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				placements = append(placements, rows...)
			}
			beforeCount := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`)

			if err := restartResource(t, f, f.envID, region, genDb.ScopeWrite); err != nil {
				t.Fatal(err)
			}
			for _, before := range placements {
				after, err := f.queries.GetResourceEnvironmentPlacement(
					t.Context(),
					genDb.GetResourceEnvironmentPlacementParams{EnvironmentID: before.EnvironmentID,
						ResourceID: f.resourceID, ClusterID: before.ClusterID,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				wantRevision := before.DesiredRevision
				restarted := before.EnvironmentID == f.envID && (region == "" || before.Region == region)
				if restarted {
					wantRevision++
				}
				if after.DesiredRevision != wantRevision || *after.DeploymentID != *before.DeploymentID ||
					!bytes.Equal(after.DesiredSpec, before.DesiredSpec) {
					t.Fatalf("placement %s changed spec/deployment or has revision %d, want %d",
						before.ID, after.DesiredRevision, wantRevision)
				}
				if (after.RestartedAt != nil) != restarted {
					t.Fatalf("placement %s restarted at %v, restarted = %v", before.ID, after.RestartedAt, restarted)
				}
				if !restarted {
					continue
				}
				spec, err := restartedSpec(after)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := decodePlacement(genDb.Placement{ID: after.ID, DesiredSpec: spec})
				if err != nil {
					t.Fatal(err)
				}
				if got := payload.AppSpec.RestartedAt; got != after.RestartedAt.UTC().Format(time.RFC3339Nano) {
					t.Fatalf("sent restart time = %q, want %v", got, after.RestartedAt)
				}
			}
			if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != beforeCount {
				t.Fatalf("restart created a deployment: count = %d", n)
			}
			for _, deployment := range f.activeDeployments(t, f.envID) {
				want := genDb.DeploymentStatusRunning
				if region == "" || deployment.Region == region {
					want = genDb.DeploymentStatusDeploying
				}
				if deployment.Status != want {
					t.Fatalf("region %s status = %s, want %s", deployment.Region, deployment.Status, want)
				}
			}
			var eventEnvironment string
			var indexedEnvironment uuid.UUID
			eventQuery := `SELECT environment_id, data->>$2 FROM events WHERE type = $1`
			row := f.pool.QueryRow(t.Context(), eventQuery, events.ResourceRestarted, events.FieldEnvironmentID)
			if err := row.Scan(&indexedEnvironment, &eventEnvironment); err != nil {
				t.Fatal(err)
			}
			if indexedEnvironment != f.envID || eventEnvironment != f.envID.String() {
				t.Fatalf("restart event environment = %s (indexed %s), want %s",
					eventEnvironment, indexedEnvironment, f.envID)
			}
		})
	}
}

func TestRestartResourceRejectsInvalidTargetsAndReadScope(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.addStagingEnvironment(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	wantCode(t, restartResource(t, f, f.envID, "", genDb.ScopeRead), connect.CodePermissionDenied)
	wantCode(t, restartResource(t, f, uuid.Nil, "", genDb.ScopeWrite), connect.CodeNotFound)
	wantCode(t, restartResource(t, f, stagingID, "", genDb.ScopeWrite), connect.CodeNotFound)
	wantCode(t, restartResource(t, f, f.envID, "missing", genDb.ScopeWrite), connect.CodeInvalidArgument)
	if n := f.count(t, `SELECT count(*) FROM events WHERE subject_id = $1 AND type = 'resource.restarted'`); n != 0 {
		t.Fatalf("rejected restarts recorded %d events", n)
	}
}

func TestDeployAfterARestartKeepsTheRestartTime(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	deploy := func() {
		t.Helper()
		if _, err := deployRequest(t, f, &deploymentv1.CreateDeploymentRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			Spec: &deploymentv1.DeploymentSpec{
				Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
			},
		}); err != nil {
			t.Fatal(err)
		}
		f.finishRollouts(t)
	}
	deploy()
	if err := restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	restarted := f.placement(t, f.clusterID)
	deploy()
	after := f.placement(t, f.clusterID)
	if *after.DeploymentID == *restarted.DeploymentID {
		t.Fatal("the deploy kept the restarted deployment")
	}
	if after.RestartedAt == nil || !after.RestartedAt.Equal(*restarted.RestartedAt) {
		t.Fatalf("restart time after the deploy = %v, want %v", after.RestartedAt, restarted.RestartedAt)
	}
}
