package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

// regionInputs are the arguments of region_status_cause and region_status.
type regionInputs struct {
	placement genDb.PlacementStatus
	ready     int32
	// heartbeatAge is how long ago the cluster last reported; nil when it never has.
	heartbeatAge *time.Duration
	health       *string
	unserved     int32
}

func freshRegion(placement genDb.PlacementStatus, ready int32) regionInputs {
	return regionInputs{
		placement:    placement,
		ready:        ready,
		heartbeatAge: new(testFreshHeartbeat),
		health:       new(clusterHealthHealthy),
	}
}

// sqlRegionStatus returns the cause and status the SQL functions derive for in.
func sqlRegionStatus(t *testing.T, pool *pgxpool.Pool, in regionInputs) (string, string) {
	t.Helper()
	var age *float64
	if in.heartbeatAge != nil {
		age = new(in.heartbeatAge.Seconds())
	}
	var cause, status string
	err := pool.QueryRow(context.Background(), `
SELECT c.cause, region_status(c.cause, $4, $5, $6::placement_status)
FROM (SELECT region_status_cause(
    now() - make_interval(secs => $1::float8), $2::text, make_interval(secs => $3::float8),
    $4, $5, $6::placement_status, $7
) AS cause) c`,
		age, in.health, testClusterSilentAfter.Seconds(), in.ready, int32(testDesiredReplicas), string(in.placement),
		in.unserved,
	).Scan(&cause, &status)
	if err != nil {
		t.Fatalf("region status of %+v: %v", in, err)
	}
	return cause, status
}

func TestRegionStatusSQL(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	degraded := resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	deploying := resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	unknown := resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
	with := func(in regionInputs, change func(*regionInputs)) regionInputs {
		change(&in)
		return in
	}

	tests := []struct {
		name  string
		in    regionInputs
		cause string
		want  resourcev1.ResourceStatus
	}{
		{name: "build before placement", in: freshRegion(genDb.PlacementStatusPending, 0), want: deploying},
		{name: "every desired replica ready", in: freshRegion(genDb.PlacementStatusRunning, 2), want: healthy},
		{name: "surge replicas during a rollout", in: freshRegion(genDb.PlacementStatusDeploying, 3), want: healthy},
		{
			name: "running deployment whose pods all crashed",
			in:   freshRegion(genDb.PlacementStatusRunning, 0), want: unavailable,
		},
		{
			name: "every replica ready but one restarted within the health window",
			in:   freshRegion(genDb.PlacementStatusDegraded, 2), want: degraded,
		},
		{
			name: "service stopped after too many restarts",
			in:   freshRegion(genDb.PlacementStatusCrashed, 0), want: unavailable,
		},
		{
			name: "running deployment that lost a replica",
			in:   freshRegion(genDb.PlacementStatusRunning, 1),
			want: degraded,
		},
		{
			name: "first rollout before any replica is ready",
			in:   freshRegion(genDb.PlacementStatusDeploying, 0), want: deploying,
		},
		{
			name: "failed redeploy while the previous replicas still serve",
			in:   freshRegion(genDb.PlacementStatusFailed, 2), want: healthy,
		},
		{
			name: "failed deployment with nothing serving",
			in:   freshRegion(genDb.PlacementStatusFailed, 0),
			want: unavailable,
		},
		{
			name: "healthy report from a cluster that stopped heartbeating",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.heartbeatAge = new(testStaleHeartbeat)
			}),
			cause: statusCauseClusterSilent, want: unknown,
		},
		{
			name: "heartbeat exactly at the threshold",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.heartbeatAge = new(testClusterSilentAfter)
			}),
			want: healthy,
		},
		{
			name: "cluster that never heartbeated",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.heartbeatAge = nil
			}),
			cause: statusCauseClusterNeverReported, want: unknown,
		},
		{
			name: "cluster whose controller is down",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.health = new(clusterHealthDegraded)
			}),
			cause: statusCauseClusterUnhealthy, want: unknown,
		},
		{
			name: "unhealthy cluster",
			in: with(freshRegion(genDb.PlacementStatusRunning, 0), func(in *regionInputs) {
				in.health = new(clusterHealthUnhealthy)
			}),
			cause: statusCauseClusterUnhealthy, want: unknown,
		},
		{
			name: "cluster without a health status",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.health = nil
			}),
			cause: statusCauseClusterUnhealthy, want: unknown,
		},
		{
			name: "healthy region whose cluster does not serve its domains",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.unserved = 1
			}),
			cause: statusCauseDomainsUnserved, want: degraded,
		},
		{
			name: "short region whose cluster does not serve its domains",
			in: with(freshRegion(genDb.PlacementStatusRunning, 1), func(in *regionInputs) {
				in.unserved = 1
			}),
			want: degraded,
		},
		{
			name: "down region whose cluster does not serve its domains",
			in: with(freshRegion(genDb.PlacementStatusRunning, 0), func(in *regionInputs) {
				in.unserved = 1
			}),
			want: unavailable,
		},
		{
			name: "silent cluster that does not serve the domains",
			in: with(freshRegion(genDb.PlacementStatusRunning, 2), func(in *regionInputs) {
				in.heartbeatAge = new(testStaleHeartbeat)
				in.unserved = 1
			}),
			cause: statusCauseClusterSilent, want: unknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause, status := sqlRegionStatus(t, pool, tt.in)
			if cause != tt.cause || status != tt.want.String() {
				t.Errorf("cause, status = %q, %s; want %q, %s", cause, status, tt.cause, tt.want)
			}
		})
	}
}

func TestRegionStatusSQLNeverOverstatesHealth(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	placements := []genDb.PlacementStatus{
		genDb.PlacementStatusPending,
		genDb.PlacementStatusDeploying,
		genDb.PlacementStatusRunning,
		genDb.PlacementStatusFailed,
	}
	heartbeats := []*time.Duration{nil, new(testFreshHeartbeat), new(testStaleHeartbeat)}
	healths := []*string{nil, new(clusterHealthHealthy), new(clusterHealthDegraded), new(clusterHealthUnhealthy)}
	readies := []int32{0, 1, testDesiredReplicas, testDesiredReplicas + 1}

	for _, placement := range placements {
		for _, heartbeat := range heartbeats {
			for _, health := range healths {
				for _, ready := range readies {
					in := regionInputs{placement: placement, ready: ready, heartbeatAge: heartbeat, health: health}
					_, status := sqlRegionStatus(t, pool, in)
					if want := expectedRegionStatus(in); status != want.String() {
						t.Errorf("%s, heartbeat %v ago, health %v, ready %d/%d: status %s, want %s",
							placement, heartbeat, health, ready, testDesiredReplicas, status, want)
					}
				}
			}
		}
	}
}

func expectedRegionStatus(in regionInputs) resourcev1.ResourceStatus {
	fresh := in.heartbeatAge != nil && *in.heartbeatAge <= testClusterSilentAfter &&
		in.health != nil && *in.health == clusterHealthHealthy
	inFlight := in.placement == genDb.PlacementStatusPending || in.placement == genDb.PlacementStatusDeploying
	switch {
	case !fresh:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
	case in.ready >= testDesiredReplicas:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	case in.ready > 0:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	case inFlight:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	default:
		return resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	}
}

func TestEnvironmentStatusSQL(t *testing.T) {
	t.Parallel()
	pool := dbtest.NewPool(t)
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY.String()
	deploying := resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING.String()
	degraded := resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED.String()
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE.String()
	unknown := resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN.String()
	unset := resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED.String()

	tests := []struct {
		name    string
		regions []string
		want    *string
	}{
		{name: "one healthy region", regions: []string{healthy}, want: &healthy},
		{name: "every region healthy", regions: []string{healthy, healthy}, want: &healthy},
		{name: "every region down", regions: []string{unavailable, unavailable}, want: &unavailable},
		{name: "every region deploying", regions: []string{deploying, deploying}, want: &deploying},
		{name: "every region unknown", regions: []string{unknown, unknown}, want: &unknown},
		{name: "every region degraded", regions: []string{degraded, degraded}, want: &degraded},
		{name: "one region down", regions: []string{healthy, unavailable}, want: &degraded},
		{name: "one region short", regions: []string{healthy, degraded}, want: &degraded},
		{name: "new region rolling out", regions: []string{healthy, deploying}, want: &deploying},
		{name: "one region silent", regions: []string{healthy, unknown}, want: &unknown},
		{name: "silent and down", regions: []string{unknown, unavailable}, want: &degraded},
		{name: "rolling out and down", regions: []string{deploying, unavailable}, want: &degraded},
		{name: "no regions", regions: []string{}},
		{name: "unset region", regions: []string{unset}},
		{name: "unset region beside a failing one", regions: []string{unset, unavailable}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *string
			err := pool.QueryRow(context.Background(), `SELECT environment_status($1::text[])`, tt.regions).Scan(&got)
			if err != nil {
				t.Fatalf("environment status: %v", err)
			}
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Errorf("environment_status(%v) = %v, want %v", tt.regions, nullable(got), nullable(tt.want))
			}
		})
	}
}

func nullable(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}
