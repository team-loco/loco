package service

import (
	"context"
	"errors"
	"fmt"

	genDb "github.com/team-loco/loco/api/gen/db"
)

var errNoActiveCluster = errors.New("no healthy cluster for the region and environment type")

// eligibleClusters lists the clusters an environment of the given type deploys to, by region.
// ListEligibleClusters holds the one predicate, so Plan's region check and every deploy agree.
func eligibleClusters(
	ctx context.Context,
	queries genDb.Querier,
	tier string,
) ([]genDb.ListEligibleClustersRow, error) {
	clusters, err := queries.ListEligibleClusters(ctx, tier)
	if err != nil {
		return nil, fmt.Errorf("list eligible clusters: %w", err)
	}
	return clusters, nil
}

// clusterForRegion returns the cluster a deployment to the region lands on: the default
// cluster first, then the oldest.
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
) (genDb.ListEligibleClustersRow, error) {
	clusters, err := eligibleClusters(ctx, queries, tier)
	if err != nil {
		return genDb.ListEligibleClustersRow{}, err
	}
	cluster, found := clusterForRegion(clusters, region)
	if !found {
		return genDb.ListEligibleClustersRow{}, fmt.Errorf("%w: %s (%s)", errNoActiveCluster, region, tier)
	}
	return cluster, nil
}
