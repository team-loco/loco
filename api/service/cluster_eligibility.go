package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpcerr"
)

const missedHeartbeats = 3

var errNoActiveCluster = rpcerr.NewCoded(
	connect.CodeFailedPrecondition,
	"no healthy application-enabled cluster for the region and environment type",
)

func ClusterSilentAfter(heartbeatInterval time.Duration) time.Duration {
	return missedHeartbeats * heartbeatInterval
}

// eligibleClusters returns, for each region with an eligible cluster, the cluster an environment of
// the given type deploys to there: the default cluster first, then the oldest. ListEligibleClusters
// holds the one predicate and orders its rows so that cluster comes first within its region, so
// Plan's region check and every deploy agree.
func eligibleClusters(
	ctx context.Context,
	queries genDb.Querier,
	tier string,
	silentAfter time.Duration,
) ([]genDb.ListEligibleClustersRow, error) {
	heardSince := time.Now().Add(-silentAfter)
	clusters, err := queries.ListEligibleClusters(ctx, genDb.ListEligibleClustersParams{
		Tier:       tier,
		HeardSince: heardSince,
	})
	if err != nil {
		return nil, fmt.Errorf("list eligible clusters: %w", err)
	}
	return slices.CompactFunc(clusters, func(a, b genDb.ListEligibleClustersRow) bool {
		return a.Region == b.Region
	}), nil
}

// clusterForRegion returns the cluster a deployment to the region lands on, from the
// per-region winners eligibleClusters returns.
func clusterForRegion(clusters []genDb.ListEligibleClustersRow, region string) (genDb.ListEligibleClustersRow, bool) {
	for _, cluster := range clusters {
		if cluster.Region == region {
			return cluster, true
		}
	}
	return genDb.ListEligibleClustersRow{}, false
}

// eligibleCluster is clusterForRegion over a fresh listing.
func eligibleCluster(
	ctx context.Context,
	queries genDb.Querier,
	region string,
	tier string,
	silentAfter time.Duration,
) (genDb.ListEligibleClustersRow, error) {
	clusters, err := eligibleClusters(ctx, queries, tier, silentAfter)
	if err != nil {
		return genDb.ListEligibleClustersRow{}, err
	}
	cluster, found := clusterForRegion(clusters, region)
	if !found {
		return genDb.ListEligibleClustersRow{}, errNoActiveCluster.Detail("%s (%s)", region, tier)
	}
	return cluster, nil
}
