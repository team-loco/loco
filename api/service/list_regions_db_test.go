package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const moveCluster = `UPDATE clusters SET region = $2 WHERE id = $1`

func (f *deployFixture) markClusterUnhealthy(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	update := `UPDATE cluster_heartbeats SET health_status = 'unhealthy' WHERE cluster_id = $1`
	if _, err := f.pool.Exec(context.Background(), update, clusterID); err != nil {
		t.Fatalf("mark cluster unhealthy: %v", err)
	}
}

func listRegions(
	t *testing.T,
	server *ResourceServer,
	scopes []genDb.EntityScope,
	environmentID string,
) []*resourcev1.RegionInfo {
	t.Helper()
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	resp, err := server.ListRegions(ctx, &resourcev1.ListRegionsRequest{EnvironmentId: environmentID})
	if err != nil {
		t.Fatalf("ListRegions %s: %v", environmentID, err)
	}
	return resp.GetRegions()
}

func TestListRegionsUsesTheSameEligibleClustersForEveryEnvironment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	f.markClusterUnhealthy(t, f.clusterID)
	if _, err := f.pool.Exec(t.Context(), moveCluster, f.clusterID, statusSecondRegion); err != nil {
		t.Fatal(err)
	}
	f.addRegionCluster(t)
	staging := f.addStagingEnvironment(t)
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		nil,
	)
	scopes := f.workspaceReadScopes(t)

	production := listRegions(t, server, scopes, f.envID.String())
	if len(production) != 2 {
		t.Fatalf("production regions = %v, want both eligible regions including %s", production, testRegion)
	}
	if production[0].GetRegion() != testRegion || production[1].GetRegion() != statusSecondRegion {
		t.Fatalf("regions = %v, want both regions in code order", production)
	}
	for _, region := range production {
		if region.GetHealthStatus() != "healthy" ||
			region.GetAvailability() != resourcev1.RegionAvailability_REGION_AVAILABILITY_AVAILABLE {
			t.Fatalf("region = %v, want a healthy available worker", region)
		}
	}
	stagingRegions := listRegions(t, server, scopes, staging.String())
	if !slices.EqualFunc(
		production,
		stagingRegions,
		func(a, b *resourcev1.RegionInfo) bool { return proto.Equal(a, b) },
	) {
		t.Fatalf("staging regions = %v, want the same workers as production %v", stagingRegions, production)
	}

	resourceScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeAdmin},
	}
	denied := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: resourceScopes})
	_, err := server.ListRegions(denied, &resourcev1.ListRegionsRequest{EnvironmentId: f.envID.String()})
	wantCode(t, err, connect.CodeNotFound)
}

func TestListRegionsReportsDegradedRegions(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		nil,
	)
	scopes := f.workspaceReadScopes(t)

	ctx := t.Context()
	update := `UPDATE cluster_heartbeats SET health_status = 'degraded' WHERE cluster_id = $1`
	if _, err := f.pool.Exec(ctx, update, f.clusterID); err != nil {
		t.Fatalf("mark default cluster degraded: %v", err)
	}

	regions := listRegions(t, server, scopes, f.envID.String())
	if len(regions) != 1 || regions[0].GetHealthStatus() != clusterHealthHealthy {
		t.Fatalf("regions with a healthy fallback = %v, want the healthy cluster", regions)
	}

	if _, err := f.pool.Exec(ctx, moveCluster, f.clusterID, statusSecondRegion); err != nil {
		t.Fatalf("move degraded cluster to its own region: %v", err)
	}
	regions = listRegions(t, server, scopes, f.envID.String())
	if len(regions) != 2 || regions[0].GetRegion() != testRegion ||
		regions[1].GetRegion() != statusSecondRegion ||
		regions[1].GetHealthStatus() != clusterHealthDegraded {
		t.Fatalf("regions = %v, want the healthy region first and the degraded region second", regions)
	}

	_, err := eligibleCluster(ctx, f.queries, statusSecondRegion, testClusterSilentAfter, testPlatform)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestListRegionsReportsOfflineClusters(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	server := NewResourceServer(
		f.pool,
		f.queries,
		testServiceDefaults(),
		testClusterSilentAfter,
		limits.Defaults(),
		nil,
	)
	scopes := f.workspaceReadScopes(t)

	silent := `DELETE FROM cluster_heartbeats WHERE cluster_id = $1`
	if _, err := f.pool.Exec(t.Context(), silent, f.clusterID); err != nil {
		t.Fatal(err)
	}
	regions := listRegions(t, server, scopes, f.envID.String())
	if len(regions) != 1 ||
		regions[0].GetAvailability() != resourcev1.RegionAvailability_REGION_AVAILABILITY_AVAILABLE {
		t.Fatalf("regions = %v, want the available fallback cluster", regions)
	}

	if _, err := f.pool.Exec(t.Context(), moveCluster, f.clusterID, statusSecondRegion); err != nil {
		t.Fatal(err)
	}
	setHeartbeats(t, f.pool, time.Now().Add(-24*time.Hour), clusterHealthHealthy, f.clusterID)
	regions = listRegions(t, server, scopes, f.envID.String())
	if len(regions) != 2 || regions[1].GetRegion() != statusSecondRegion ||
		regions[1].GetAvailability() != resourcev1.RegionAvailability_REGION_AVAILABILITY_OFFLINE {
		t.Fatalf("regions = %v, want the offline region second", regions)
	}
}
