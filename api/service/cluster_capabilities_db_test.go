package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func TestClusterCapabilitiesSeparateBuildsAndApplications(t *testing.T) {
	f := newBuildFixture(t)
	f.markClustersHealthy(t)
	admin := NewClusterAdminServer(f.pool, f.queries, testAgentConfig().JoinTokenTTL)
	update := func(id uuid.UUID, applications, builds bool) {
		t.Helper()
		_, err := admin.UpdateCluster(systemAdminContext(t), &clusterv1.UpdateClusterRequest{
			ClusterId: id.String(), ApplicationsEnabled: applications, BuildsAllowed: builds,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	update(f.clusterID, false, true)
	update(f.otherCluster, true, false)
	buildRegionQuery := "UPDATE clusters SET region = 'builds-region' WHERE id = $1"
	if _, err := f.pool.Exec(t.Context(), buildRegionQuery, f.clusterID); err != nil {
		t.Fatal(err)
	}
	regions, err := f.queries.ListClusterRegions(t.Context(), genDb.ListClusterRegionsParams{
		Tier: environmentTypeProduction, HeardSince: time.Now().Add(-testClusterSilentAfter),
	})
	if err != nil || len(regions) != 1 || regions[0].Region != testRegion {
		t.Fatalf("application regions = %v, err = %v", regions, err)
	}

	clusters, err := eligibleClusters(t.Context(), f.queries, environmentTypeProduction, testClusterSilentAfter)
	if err != nil || len(clusters) != 1 || clusters[0].ID != f.otherCluster {
		t.Fatalf("application clusters = %v, err = %v", clusters, err)
	}
	planned, err := plan(t, f.deployFixture, planFileHeader+planFileNew, f.workspaceReadScopes(t))
	if err != nil || len(planned.GetErrors()) != 0 {
		t.Fatalf("plan = %v, err = %v", planned, err)
	}
	build := f.create(t, 100)
	f.bucket.upload("sources/"+build.GetBuildId()+".tar.gz", 100)
	started, err := f.start(build.GetBuildId())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.queries.GetBuildByID(t.Context(), uuid.MustParse(started.GetId()))
	if err != nil || stored.ClusterID == nil || *stored.ClusterID != f.clusterID {
		t.Fatalf("build = %v, err = %v", stored, err)
	}
	_, err = f.deploy(t.Context(), staticSpec)
	requireCode(t, err, connect.CodeFailedPrecondition, "direct placement on build-only cluster")
	if !strings.Contains(err.Error(), "does not accept applications") {
		t.Fatal(err)
	}
	deploymentID, err := f.deployTo(t.Context(), f.otherCluster, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	update(f.otherCluster, false, false)
	_, err = createDeployment(t, f.deployFixture, sameRegionSpec)
	requireCode(t, err, connect.CodeFailedPrecondition, "no runtime cluster")
	if !strings.Contains(err.Error(), "application-enabled") {
		t.Fatal(err)
	}
	planned, err = plan(t, f.deployFixture, planFileHeader+planFileNew, f.workspaceReadScopes(t))
	if err != nil || len(planned.GetErrors()) == 0 ||
		!strings.Contains(planned.GetErrors()[0].GetMessage(), "application-enabled") {
		t.Fatalf("plan = %v, err = %v", planned, err)
	}
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusPending {
		t.Fatalf("existing deployment changed: %s", status)
	}
	if placement := f.placement(t, f.otherCluster); placement.DesiredDeleted {
		t.Fatal("capability change deleted existing placement")
	}
	update(f.clusterID, false, false)
	if _, lookupErr := f.queries.GetBuildCluster(t.Context()); !errors.Is(lookupErr, pgx.ErrNoRows) {
		t.Fatalf("build-disabled selection: %v", lookupErr)
	}
	stored, err = f.queries.GetBuildByID(t.Context(), uuid.MustParse(started.GetId()))
	if err != nil || stored.Status != "queued" {
		t.Fatalf("admitted build changed: %v, %v", stored, err)
	}
}

func TestClusterRegistrationStoresCapabilities(t *testing.T) {
	e := newEnrollmentFixture(t)
	ctx := systemAdminContext(t)
	request := &clusterv1.CreateClusterRequest{
		RequestId: uuid.NewString(), Name: "builds", Region: testRegion,
		Tier:                environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
		ApplicationsEnabled: false, BuildsAllowed: true,
	}
	created, err := e.admin.CreateCluster(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := e.admin.ListClusters(ctx, &clusterv1.ListClustersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cluster := range listed.GetClusters() {
		if cluster.GetId() != created.GetClusterId() {
			continue
		}
		if cluster.GetApplicationsEnabled() || !cluster.GetBuildsAllowed() || cluster.GetBuildsEnabled() {
			t.Fatal(cluster)
		}
		return
	}
	t.Fatal("registered cluster missing from list")
}
