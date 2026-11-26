package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

const (
	firstCalifornia  = "us-ca-1"
	secondCalifornia = "us-ca-2"
)

type regionFixture struct {
	pool     *pgxpool.Pool
	clusters *ClusterAdminServer
	regions  *RegionAdminServer
	ctx      context.Context
}

func newRegionFixture(t *testing.T) *regionFixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	queries := genDb.New(pool)
	return &regionFixture{
		pool: pool,
		clusters: NewClusterAdminServer(
			pool,
			queries,
			testAgentConfig().JoinTokenTTL,
			testAgentConfig().JoinTokenMaxTTL, testHeartbeatInterval),
		regions: NewRegionAdminServer(queries),
		ctx:     systemAdminContext(t),
	}
}

func newRegionRequest(country, subdivision string) *clusterv1.CreateClusterRequest {
	return &clusterv1.CreateClusterRequest{
		RequestId: uuid.NewString(),
		Target: &clusterv1.CreateClusterRequest_NewRegion{NewRegion: &clusterv1.NewRegion{
			Country: country, Subdivision: subdivision,
		}},
	}
}

func inRegion(code string) *clusterv1.CreateClusterRequest_Region {
	return &clusterv1.CreateClusterRequest_Region{Region: code}
}

func inRegionRequest(code, name string) *clusterv1.CreateClusterRequest {
	return &clusterv1.CreateClusterRequest{RequestId: uuid.NewString(), Name: name, Target: inRegion(code)}
}

func (f *regionFixture) create(t *testing.T, req *clusterv1.CreateClusterRequest) *clusterv1.CreateClusterResponse {
	t.Helper()
	resp, err := f.clusters.CreateCluster(f.ctx, req)
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}
	return resp
}

func (f *regionFixture) list(t *testing.T) map[string]*clusterv1.Region {
	t.Helper()
	resp, err := f.regions.ListRegions(f.ctx, &clusterv1.ListRegionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	regions := map[string]*clusterv1.Region{}
	for _, region := range resp.GetRegions() {
		regions[region.GetCode()] = region
	}
	return regions
}

func TestCreateClusterBuildsRegionCodesFromCountryAndSubdivision(t *testing.T) {
	t.Parallel()
	f := newRegionFixture(t)

	first := f.create(t, newRegionRequest("us", "ca"))
	if first.GetRegion() != firstCalifornia || first.GetName() != firstCalifornia {
		t.Fatalf("first California cluster = %s in %s, want us-ca-1 in us-ca-1", first.GetName(), first.GetRegion())
	}
	second := f.create(t, newRegionRequest("us", "ca"))
	tokyo := f.create(t, newRegionRequest("jp", "13"))
	if second.GetRegion() != secondCalifornia || tokyo.GetRegion() != "jp-13-1" {
		t.Fatalf("regions = %s, %s; want us-ca-2 and jp-13-1", second.GetRegion(), tokyo.GetRegion())
	}

	_, err := f.clusters.CreateCluster(f.ctx, inRegionRequest(firstCalifornia, ""))
	requireCode(t, err, connect.CodeInvalidArgument, "unnamed second cluster")
	_, err = f.clusters.CreateCluster(f.ctx, inRegionRequest("us-tx-1", "austin"))
	requireCode(t, err, connect.CodeNotFound, "join a region without clusters")
	added := f.create(t, inRegionRequest(firstCalifornia, "us-ca-1-b"))
	if added.GetRegion() != firstCalifornia || added.GetName() != "us-ca-1-b" {
		t.Fatalf("added cluster = %s in %s, want us-ca-1-b in us-ca-1", added.GetName(), added.GetRegion())
	}

	regions := f.list(t)
	if len(regions) != 3 || regions[firstCalifornia].GetClusterCount() != 2 ||
		regions[secondCalifornia].GetClusterCount() != 1 {
		t.Fatalf("regions = %v, want us-ca-1 with both its clusters", regions)
	}
}

func TestCreateClusterRefusesRegionsThatAreNotDNSLabels(t *testing.T) {
	t.Parallel()
	f := newRegionFixture(t)
	for _, spec := range []*clusterv1.NewRegion{
		{Country: "US", Subdivision: "ca"},
		{Country: "us", Subdivision: "c-a"},
		{Country: "us"},
	} {
		req := &clusterv1.CreateClusterRequest{
			RequestId: uuid.NewString(),
			Target:    &clusterv1.CreateClusterRequest_NewRegion{NewRegion: spec},
		}
		_, err := f.clusters.CreateCluster(f.ctx, req)
		requireCode(t, err, connect.CodeInvalidArgument, spec.String())
	}
}

func TestCreateClusterRetryKeepsItsNewRegion(t *testing.T) {
	t.Parallel()
	f := newRegionFixture(t)
	req := newRegionRequest("de", "he")
	first := f.create(t, req)
	retry := f.create(t, req)
	if retry.GetClusterId() != first.GetClusterId() || retry.GetRegion() != "de-he-1" {
		t.Fatalf("retry = %v, want the same cluster in de-he-1", retry)
	}
	if regions := f.list(t); len(regions) != 1 {
		t.Fatalf("regions = %v, want the retry to reuse its region", regions)
	}
	req.GetNewRegion().Subdivision = "by"
	_, err := f.clusters.CreateCluster(f.ctx, req)
	requireCode(t, err, connect.CodeFailedPrecondition, "retry naming another place")
}

func TestLocalClustersShareLocal1(t *testing.T) {
	t.Parallel()
	f := newRegionFixture(t)
	first := f.create(t, inRegionRequest(localRegion, ""))
	second := f.create(t, inRegionRequest(localRegion, "builds"))
	if first.GetRegion() != localRegion || first.GetName() != localRegion || second.GetRegion() != localRegion {
		t.Fatalf("local clusters = %v, %v; want both in local-1", first, second)
	}
	if region := f.list(t)[localRegion]; region.GetClusterCount() != 2 {
		t.Fatalf("local region = %v, want both clusters", region)
	}
}
