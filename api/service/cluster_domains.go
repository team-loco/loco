package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

func regionClusters(clusters []genDb.ListEligibleClustersRow) []configplan.Cluster {
	byRegion := make([]configplan.Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		first, _ := clusterForRegion(clusters, cluster.Region)
		if first.ID != cluster.ID {
			continue
		}
		byRegion = append(
			byRegion,
			configplan.Cluster{Name: cluster.Name, Region: cluster.Region, Domains: cluster.Domains},
		)
	}
	return byRegion
}

type domainCheck struct {
	clusters []configplan.Cluster
	targets  []configplan.Cluster
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
	targets := map[uuid.UUID]configplan.Cluster{}
	for _, region := range regions {
		cluster, found := clusterForRegion(eligible, region)
		if !found {
			continue
		}
		targets[cluster.ID] = configplan.Cluster{Name: cluster.Name, Region: cluster.Region, Domains: cluster.Domains}
	}
	placed, err := q.ListClusterDomains(ctx, placedClusters)
	if err != nil {
		return domainCheck{}, fmt.Errorf("list placement cluster domains: %w", err)
	}
	for _, cluster := range placed {
		targets[cluster.ID] = configplan.Cluster{Name: cluster.Name, Region: cluster.Region, Domains: cluster.Domains}
	}
	ordered := slices.SortedFunc(maps.Values(targets), func(a, b configplan.Cluster) int {
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

func (c domainCheck) check(hostname string) (string, string, error) {
	platform, label, err := configplan.CheckDomain(hostname, c.clusters, c.targets)
	if errors.Is(err, configplan.ErrDomainNotServed) {
		return "", "", rpcerr.New(connect.CodeFailedPrecondition, err)
	}
	if err != nil {
		return "", "", rpcerr.New(connect.CodeInvalidArgument, err)
	}
	return platform, label, nil
}
