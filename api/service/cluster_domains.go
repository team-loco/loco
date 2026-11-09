package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/platformdomain"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func platformCluster(cluster genDb.ListEligibleClustersRow) platformdomain.Cluster {
	return platformdomain.Cluster{Name: cluster.Name, Region: cluster.Region, Domains: cluster.Domains}
}

func regionClusters(eligible []genDb.ListEligibleClustersRow) []platformdomain.Cluster {
	clusters := make([]platformdomain.Cluster, 0, len(eligible))
	for _, cluster := range eligible {
		clusters = append(clusters, platformCluster(cluster))
	}
	return clusters
}

type domainCheck struct {
	clusters []platformdomain.Cluster
	targets  []platformdomain.Cluster
}

func resourceDomainCheck(
	ctx context.Context,
	q genDb.Querier,
	tier string,
	regions []string,
	placedClusters []uuid.UUID,
	silentAfter time.Duration,
) (domainCheck, error) {
	eligible, err := eligibleClusters(ctx, q, tier, silentAfter)
	if err != nil {
		return domainCheck{}, err
	}
	targets := map[uuid.UUID]platformdomain.Cluster{}
	for _, region := range regions {
		cluster, found := clusterForRegion(eligible, region)
		if !found {
			continue
		}
		targets[cluster.ID] = platformCluster(cluster)
	}
	placed, err := q.ListClusterDomains(ctx, placedClusters)
	if err != nil {
		return domainCheck{}, fmt.Errorf("list placement cluster domains: %w", err)
	}
	for _, cluster := range placed {
		targets[cluster.ID] = platformdomain.Cluster{
			Name:    cluster.Name,
			Region:  cluster.Region,
			Domains: cluster.Domains,
		}
	}
	ordered := slices.SortedFunc(maps.Values(targets), func(a, b platformdomain.Cluster) int {
		return strings.Compare(a.Region+"/"+a.Name, b.Region+"/"+b.Name)
	})
	return domainCheck{clusters: regionClusters(eligible), targets: ordered}, nil
}

func enabledRegions(regions map[string]*resourcev1.RegionTarget) []string {
	enabled := make([]string, 0, len(regions))
	for region, target := range regions {
		if target.GetEnabled() {
			enabled = append(enabled, region)
		}
	}
	slices.Sort(enabled)
	return enabled
}
