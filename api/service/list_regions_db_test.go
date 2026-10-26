package service

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func (f *deployFixture) addStagingRegionCluster(t *testing.T) {
	t.Helper()
	insert := `
INSERT INTO clusters (name, region, provider, is_active, is_default, health_status, last_heartbeat, tier)
VALUES ($1, $2, 'kind', true, true, 'healthy', NOW(), $3)`
	if _, err := f.pool.Exec(
		context.Background(),
		insert,
		domainsSecondCluster,
		statusSecondRegion,
		stagingName,
	); err != nil {
		t.Fatalf("add staging region cluster: %v", err)
	}
}

func (f *deployFixture) markClusterUnhealthy(t *testing.T, clusterID uuid.UUID) {
	t.Helper()
	update := `UPDATE clusters SET health_status = 'unhealthy' WHERE id = $1`
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
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	resp, err := server.ListRegions(ctx, &resourcev1.ListRegionsRequest{EnvironmentId: environmentID})
	if err != nil {
		t.Fatalf("ListRegions %s: %v", environmentID, err)
	}
	return resp.GetRegions()
}

func TestListRegionsListsOnlyTheRegionsOfTheEnvironmentsEligibleClusters(t *testing.T) {
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	f.markClusterUnhealthy(t, f.clusterID)
	f.addStagingRegionCluster(t)
	staging := f.addStagingEnvironment(t)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	scopes := f.workspaceReadScopes(t)

	production := listRegions(t, server, scopes, f.envID.String())
	if len(production) != 1 {
		t.Fatalf("production regions = %v, want only %s", production, testRegion)
	}
	region := production[0]
	if region.GetRegion() != testRegion || region.GetIsDefault() || region.GetHealthStatus() != "healthy" {
		t.Fatalf("production region = %v, want %s from c2: not default, healthy", region, testRegion)
	}

	stagingRegions := listRegions(t, server, scopes, staging.String())
	names := make([]string, 0, len(stagingRegions))
	for _, info := range stagingRegions {
		names = append(names, info.GetRegion())
	}
	if !slices.Equal(names, []string{statusSecondRegion}) || !stagingRegions[0].GetIsDefault() {
		t.Fatalf("staging regions = %v, want %s as the default", stagingRegions, statusSecondRegion)
	}

	resourceScopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeAdmin},
	}
	denied := context.WithValue(context.Background(), contextkeys.EntityScopesKey, resourceScopes)
	_, err := server.ListRegions(denied, &resourcev1.ListRegionsRequest{EnvironmentId: f.envID.String()})
	wantCode(t, err, connect.CodePermissionDenied)
}
