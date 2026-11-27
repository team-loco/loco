package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/systemtenant"
)

// CapacityCluster contains independently sampled native capacity and a complete
// reservation cohort. Nodes with unknown native dimensions remain distinguishable.
type CapacityCluster struct {
	Cluster     string
	Sample      *CapacitySample
	Nodes       []NodeCapacity
	CPU, Memory int64
	Resources   int32
}

func (c *Client) CapacityClusters(ctx context.Context, clusters []string) ([]CapacityCluster, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	samples, err := c.capacitySamples(ctx, clusters)
	if err != nil {
		return nil, err
	}
	return c.capacityClusters(ctx, clusters, samples)
}

func (c *Client) capacityClusters(
	ctx context.Context,
	clusters []string,
	samples []CapacitySample,
) ([]CapacityCluster, error) {
	native, err := c.nodeCapacities(ctx, clusters)
	if err != nil {
		return nil, err
	}
	nodes, err := c.capacityTotals(ctx, samples, CapacityScope{Group: CapacityByNode})
	if err != nil {
		return nil, err
	}
	totals, err := c.capacityTotals(ctx, samples, CapacityScope{Group: CapacityByCluster})
	if err != nil {
		return nil, err
	}
	byCluster := make(map[string]*CapacityCluster, len(clusters))
	byNode := map[string]map[string]*NodeCapacity{}
	for _, cluster := range clusters {
		byCluster[cluster] = &CapacityCluster{Cluster: cluster}
		byNode[cluster] = map[string]*NodeCapacity{}
	}
	for cluster, rows := range native {
		for _, row := range rows {
			byNode[cluster][row.Name] = new(row)
		}
	}
	for i := range samples {
		byCluster[samples[i].Cluster].Sample = &samples[i]
	}
	for _, row := range nodes {
		node := byNode[row.Cluster][row.Node]
		if node == nil {
			node = &NodeCapacity{Name: row.Node, Stale: true}
			byNode[row.Cluster][row.Node] = node
		}
		node.RequestedCPU = row.CPU + row.BuildCPU
		node.RequestedMemory = row.Memory + row.BuildMemory
		node.Resources = row.Resources
	}
	for _, row := range totals {
		cluster := byCluster[row.Cluster]
		cluster.CPU = row.CPU + row.BuildCPU
		cluster.Memory = row.Memory + row.BuildMemory
		cluster.Resources = row.Resources
	}
	out := make([]CapacityCluster, 0, len(clusters))
	for _, id := range clusters {
		cluster := byCluster[id]
		for _, node := range byNode[id] {
			cluster.Nodes = append(cluster.Nodes, *node)
		}
		out = append(out, *cluster)
	}
	return out, nil
}

var ErrCapacitySampleUnavailable = errors.New("capacity collection is unavailable")
var ErrCapacitySampleID = errors.New("invalid capacity sample ID")

func (c *Client) CapacityClusterAt(ctx context.Context, cluster, id string) (CapacityCluster, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	separator := strings.LastIndexByte(id, ':')
	if separator <= 0 {
		return CapacityCluster{}, ErrCapacitySampleID
	}
	millis, err := strconv.ParseInt(id[separator+1:], 10, 64)
	if err != nil {
		return CapacityCluster{}, ErrCapacitySampleID
	}
	sample := CapacitySample{Cluster: cluster, Source: id[:separator], At: time.UnixMilli(millis)}
	if sample.At.Before(time.Now().Add(-CapacityLookback)) {
		return CapacityCluster{}, ErrCapacitySampleUnavailable
	}
	var count uint64
	var expected float64
	var interval string
	err = c.conn.QueryRow(
		ctx,
		`SELECT count(),max(Value),any(ResourceAttributes['loco.collection.interval'])
FROM otel_metrics_gauge WHERE WorkspaceId=? AND ResourceAttributes['loco.cluster.id']=?
AND MetricName=? AND Attributes['source_id']=? AND TimeUnix=fromUnixTimestamp64Milli(?) AND bitAnd(Flags,1)=0`,
		systemtenant.WorkspaceID,
		cluster,
		capacitymetrics.Pods,
		sample.Source,
		millis,
	).Scan(&count, &expected, &interval)
	if err != nil {
		return CapacityCluster{}, err
	}
	if count == 0 {
		return CapacityCluster{}, ErrCapacitySampleUnavailable
	}
	duration, err := time.ParseDuration(interval)
	if err != nil || duration <= 0 {
		return CapacityCluster{}, fmt.Errorf("invalid capacity collection interval %q", interval)
	}
	sample.Interval = duration
	membership, err := c.capacityMembership(ctx, []CapacitySample{sample})
	if err != nil {
		return CapacityCluster{}, err
	}
	members := membership[cluster+":"+sample.ID()]
	complete := float64(members.cpu) == expected && float64(members.memory) == expected &&
		float64(members.pods) == expected
	if !complete {
		return CapacityCluster{}, ErrCapacitySampleUnavailable
	}
	rows, err := c.capacityClusters(ctx, []string{cluster}, []CapacitySample{sample})
	if err != nil {
		return CapacityCluster{}, err
	}
	return rows[0], nil
}
