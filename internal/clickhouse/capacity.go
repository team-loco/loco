package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/systemtenant"
)

// CapacityLookback bounds operational current-state reads. Older telemetry remains
// available through the historical metrics API, but is unavailable as current capacity.
const CapacityLookback = 24 * time.Hour

// CapacitySample identifies a complete immutable controller scrape in ClickHouse.
type CapacitySample struct {
	Cluster, Source string
	At              time.Time
	Interval        time.Duration
}

func (s CapacitySample) ID() string  { return s.Source + ":" + strconv.FormatInt(s.At.UnixMilli(), 10) }
func (s CapacitySample) Stale() bool { return time.Since(s.At) > 3*s.Interval }

// CapacityTotals are aggregated from actual pod requests in one complete sample.
type CapacityTotals struct {
	Cluster, Node, Organization, Workspace, Resource string
	CPU, Memory, BuildCPU, BuildMemory               int64
	Pods, Resources                                  int32
}

// CapacityScope selects a bounded operational aggregation. Empty IDs retain all observed tenants.
type CapacityGroup string

const (
	CapacityByNode      CapacityGroup = "node"
	CapacityByWorkspace CapacityGroup = "workspace"
	CapacityByResource  CapacityGroup = ResourceField
	CapacityByCluster   CapacityGroup = "cluster"
)

type CapacityScope struct {
	Node, Organization, Workspace, Environment string
	Resources                                  []string
	Workspaces                                 []string
	Group                                      CapacityGroup
}

func (c *Client) CapacityTotals(ctx context.Context, s CapacitySample, scope CapacityScope) ([]CapacityTotals, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	return c.capacityTotals(ctx, []CapacitySample{s}, scope)
}

func (c *Client) capacityTotals(
	ctx context.Context,
	samples []CapacitySample,
	scope CapacityScope,
) ([]CapacityTotals, error) {
	if len(samples) == 0 {
		return []CapacityTotals{}, nil
	}
	ctx, err := capacityCohorts(ctx, samples)
	if err != nil {
		return nil, err
	}

	var dimensions string
	switch scope.Group {
	case CapacityByNode:
		dimensions = "cluster,node,'' AS organization,'' AS workspace,'' AS resource"
	case CapacityByWorkspace:
		dimensions = "'' AS cluster,'' AS node,any(organization) AS organization,workspace,'' AS resource"
	case CapacityByCluster:
		dimensions = "cluster,'' AS node,'' AS organization,'' AS workspace,'' AS resource"
	case CapacityByResource:
		dimensions = "'' AS cluster,'' AS node,any(organization) AS organization,workspace,resource"
	default:
		return nil, errors.New("invalid capacity grouping")
	}
	args := []any{systemtenant.WorkspaceID, capacitymetrics.CPU, capacitymetrics.Memory}
	filters := []string{}
	for _, filter := range []struct{ name, value string }{
		{"node", scope.Node}, {"organization_id", scope.Organization}, {"workspace_id", scope.Workspace},
	} {
		if filter.value != "" {
			filters = append(filters, "Attributes['"+filter.name+"']=?")
			args = append(args, filter.value)
		}
	}
	if scope.Workspaces != nil {
		filters = append(filters, "has(?, Attributes['workspace_id'])")
		args = append(args, scope.Workspaces)
	}
	if scope.Resources != nil {
		filters = append(filters, "has(?, Attributes['resource_id'])")
		args = append(args, scope.Resources)
	}
	where := ""
	if len(filters) > 0 {
		where = " AND " + strings.Join(filters, " AND ")
	}
	// Runtime environment filters do not remove workspace-scoped builds. Return the
	// two amounts separately so callers cannot label build requests as environment usage.
	args = append(args, scope.Environment, scope.Environment)
	query := `WITH pods AS (
 SELECT ResourceAttributes['loco.cluster.id'] AS cluster,Attributes['pod_uid'] AS pod,any(Attributes['node']) AS node,
any(Attributes['organization_id']) AS organization,
 any(Attributes['workspace_id']) AS workspace,any(Attributes['resource_id']) AS resource,
 any(Attributes['environment_id']) AS environment,any(Attributes['build_id']) AS build,
 maxIf(Value,MetricName='` + capacitymetrics.CPU + `') AS cpu,
 maxIf(Value,MetricName='` + capacitymetrics.Memory + `') AS memory
 FROM otel_metrics_gauge WHERE ` + capacityCohortFilter + `
 AND MetricName IN (?,?)` + where + ` GROUP BY cluster,pod
 ) SELECT ` + dimensions + `,toInt64(round(sumIf(cpu,build='' AND (?='' OR environment=?))*1000)),
 toInt64(sumIf(memory,build='' AND (?='' OR environment=?))),
 toInt64(round(sumIf(cpu,build!='')*1000)),toInt64(sumIf(memory,build!='')),toInt32(count()),
 toInt32(uniqExactIf(pods.resource,pods.resource!='')) FROM pods GROUP BY cluster,node,workspace,resource`
	// Each runtime dimension uses the same environment predicate.
	args = append(args, scope.Environment, scope.Environment)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate capacity: %w", err)
	}
	defer rows.Close()
	out := []CapacityTotals{}
	for rows.Next() {
		var row CapacityTotals
		if err = rows.Scan(
			&row.Cluster,
			&row.Node,
			&row.Organization,
			&row.Workspace,
			&row.Resource,
			&row.CPU,
			&row.Memory,
			&row.BuildCPU,
			&row.BuildMemory,
			&row.Pods,
			&row.Resources,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type NodeCapacity struct {
	Name                          string
	RequestedCPU, RequestedMemory int64
	Resources                     int32
	CPU, Memory                   *int64
	Ready                         *bool
	At                            time.Time
	Stale                         bool
}

func (c *Client) nodeCapacities(ctx context.Context, clusters []string) (map[string][]NodeCapacity, error) {
	if len(clusters) == 0 {
		return map[string][]NodeCapacity{}, nil
	}

	rows, err := c.conn.Query(
		ctx,
		`SELECT ResourceAttributes['loco.cluster.id'],ResourceAttributes['k8s.node.name'],MetricName,
 argMax(Value,TimeUnix),max(TimeUnix),argMax(ResourceAttributes['loco.collection.interval'],TimeUnix)
 FROM otel_metrics_gauge WHERE WorkspaceId=? AND has(?,ResourceAttributes['loco.cluster.id'])
 AND ResourceAttributes['k8s.node.name']!=''
 AND MetricName IN ('k8s.node.allocatable_cpu','k8s.node.allocatable_memory','k8s.node.condition_ready')
 AND bitAnd(Flags,1)=0
GROUP BY ResourceAttributes['loco.cluster.id'],ResourceAttributes['k8s.node.name'],MetricName
HAVING max(TimeUnix)>=?`,
		systemtenant.WorkspaceID,
		clusters,
		time.Now().Add(-CapacityLookback),
	)
	if err != nil {
		return nil, fmt.Errorf("query node capacity: %w", err)
	}
	defer rows.Close()
	nodes := map[string]map[string]*NodeCapacity{}
	for rows.Next() {
		var cluster, name, metric, interval string
		var value float64
		var at time.Time
		if err = rows.Scan(&cluster, &name, &metric, &value, &at, &interval); err != nil {
			return nil, err
		}
		duration, parseErr := time.ParseDuration(interval)
		if parseErr != nil || duration <= 0 {
			return nil, fmt.Errorf("invalid native node collection interval %q", interval)
		}
		if nodes[cluster] == nil {
			nodes[cluster] = map[string]*NodeCapacity{}
		}
		node := nodes[cluster][name]
		if node == nil {
			node = &NodeCapacity{Name: name}
			nodes[cluster][name] = node
		}
		if node.At.IsZero() || at.Before(node.At) {
			node.At = at
		}
		node.Stale = node.Stale || time.Since(at) > 3*duration
		switch metric {
		case "k8s.node.allocatable_cpu":
			node.CPU = new(int64(math.Round(value * 1000)))
		case "k8s.node.allocatable_memory":
			node.Memory = new(int64(value))
		case "k8s.node.condition_ready":
			node.Ready = new(value != 0)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]NodeCapacity{}
	for cluster, rows := range nodes {
		for _, node := range rows {
			out[cluster] = append(out[cluster], *node)
		}
	}
	return out, nil
}

// CapacityInventory reads each cluster under one request budget and merges the
// observed reservations. Missing clusters remain absent from samples for coverage.
func (c *Client) CapacityInventory(
	ctx context.Context,
	clusters []string,
	scope CapacityScope,
) ([]CapacityTotals, []CapacitySample, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	samples, err := c.capacitySamples(ctx, clusters)
	if err != nil {
		return nil, nil, err
	}
	totals, err := c.capacityTotals(ctx, samples, scope)
	if err != nil {
		return nil, nil, err
	}
	return totals, samples, nil
}
