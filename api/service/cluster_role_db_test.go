package service

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	genDb "github.com/team-loco/loco/api/gen/db"
)

func (f *buildFixture) setRole(t *testing.T, id uuid.UUID, role genDb.ClusterRole) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "UPDATE clusters SET role = $2 WHERE id = $1", id, role); err != nil {
		t.Fatal(err)
	}
}

func TestClusterRolesSeparateBuildsAndApplications(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	f.setRole(t, f.clusterID, genDb.ClusterRoleBuilds)
	f.setRole(t, f.otherCluster, genDb.ClusterRoleApps)
	buildRegionQuery := "UPDATE clusters SET region = 'builds-region' WHERE id = $1"
	if _, err := f.pool.Exec(t.Context(), buildRegionQuery, f.clusterID); err != nil {
		t.Fatal(err)
	}
	regions, err := f.queries.ListClusterRegions(t.Context(), genDb.ListClusterRegionsParams{
		Platform:           testPlatform,
		SilentAfterSeconds: testClusterSilentAfter.Seconds(),
	})
	if err != nil || len(regions) != 1 || regions[0].Region != testRegion {
		t.Fatalf("application regions = %v, err = %v", regions, err)
	}
	clusters, err := eligibleClusters(t.Context(), f.queries, testClusterSilentAfter, testPlatform)
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
	requireCode(t, err, connect.CodeFailedPrecondition, "direct placement on a builds cluster")
	if !strings.Contains(err.Error(), "does not accept applications") {
		t.Fatal(err)
	}
	if _, deployErr := f.deployTo(t.Context(), f.otherCluster, staticSpec); deployErr != nil {
		t.Fatal(deployErr)
	}

	f.setRole(t, f.otherCluster, genDb.ClusterRoleBuilds)
	_, err = createTestDeployment(t, f.deployFixture, sameRegionSpec)
	requireCode(t, err, connect.CodeFailedPrecondition, "no apps cluster in the region")
	if !strings.Contains(err.Error(), "accepting applications") {
		t.Fatal(err)
	}
	planned, err = plan(t, f.deployFixture, planFileHeader+planFileNew, f.workspaceReadScopes(t))
	if err != nil || len(planned.GetErrors()) == 0 ||
		!strings.Contains(planned.GetErrors()[0].GetMessage(), "accepting applications") {
		t.Fatalf("plan = %v, err = %v", planned, err)
	}

	f.setRole(t, f.clusterID, genDb.ClusterRoleApps)
	f.setRole(t, f.otherCluster, genDb.ClusterRoleApps)
	if _, lookupErr := f.queries.GetBuildCluster(
		t.Context(),
		genDb.GetBuildClusterParams{SilentAfterSeconds: testClusterSilentAfter.Seconds(), Platform: testPlatform},
	); !errors.Is(
		lookupErr,
		pgx.ErrNoRows,
	) {
		t.Fatalf("build cluster among apps clusters: %v", lookupErr)
	}
}

func TestBuildClusterSelectionIgnoresTheReplicaClock(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	f.markClustersHealthy(t)
	for _, skew := range []time.Duration{time.Hour, -time.Hour} {
		f.server.now = func() time.Time { return time.Now().Add(skew) }
		id, err := f.server.buildCluster(t.Context())
		if err != nil || (id != f.clusterID && id != f.otherCluster) {
			t.Fatalf("skew %v: build cluster %v, err = %v, want a heartbeating cluster", skew, id, err)
		}
	}
}

func TestEligibilityAndRegionsJudgeHeartbeatsOnTheDatabaseClock(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	f.markClustersHealthy(t)
	for _, test := range []struct {
		age  time.Duration
		want bool
	}{
		{age: testClusterSilentAfter - 10*time.Second, want: true},
		{age: testClusterSilentAfter + 10*time.Second, want: false},
	} {
		if _, err := f.pool.Exec(t.Context(),
			`UPDATE cluster_heartbeats SET last_heartbeat = clock_timestamp() - make_interval(secs => $1)`,
			test.age.Seconds()); err != nil {
			t.Fatal(err)
		}
		clusters, err := eligibleClusters(t.Context(), f.queries, testClusterSilentAfter, testPlatform)
		if err != nil {
			t.Fatal(err)
		}
		eligible := slices.ContainsFunc(clusters, func(c genDb.ListEligibleClustersRow) bool {
			return c.ID == f.clusterID
		})
		regions, err := f.queries.ListClusterRegions(t.Context(), genDb.ListClusterRegionsParams{
			Platform:           testPlatform,
			SilentAfterSeconds: testClusterSilentAfter.Seconds(),
		})
		if err != nil {
			t.Fatal(err)
		}
		available := slices.ContainsFunc(regions, func(r genDb.ListClusterRegionsRow) bool {
			return r.Region == testRegion && r.Available
		})
		if eligible != test.want || available != test.want {
			t.Fatalf(
				"heartbeat %v old: eligible = %v, region available = %v, want %v",
				test.age, eligible, available, test.want,
			)
		}
	}
}
