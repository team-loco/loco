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

var ErrCapacityUnavailable = errors.New("no complete capacity collection in the last 24 hours")

// CapacitySample identifies a complete immutable controller scrape in ClickHouse.
type CapacitySample struct {
	Cluster, Source string
	At              time.Time
	Interval        time.Duration
}

func (s CapacitySample) ID() string  { return s.Source + ":" + strconv.FormatInt(s.At.UnixMilli(), 10) }
func (s CapacitySample) Stale() bool { return time.Since(s.At) > 3*s.Interval }

func (c *Client) CapacitySample(ctx context.Context, cluster string) (*CapacitySample, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	// A manifest can arrive before the OTLP batches containing its pods. Walk back
	// until both dimensions are complete, never interpreting an incomplete cohort as zero.
	for offset := 0; ; offset += 10 {
		rows, err := c.conn.Query(
			ctx,
			`SELECT Attributes['source_id'],TimeUnix,max(Value),any(ResourceAttributes['loco.collection.interval'])
FROM otel_metrics_gauge WHERE WorkspaceId=? AND ResourceAttributes['loco.cluster.id']=? AND MetricName=?
AND bitAnd(Flags,1)=0 AND TimeUnix>=? GROUP BY Attributes['source_id'],TimeUnix
ORDER BY TimeUnix DESC,Attributes['source_id'] LIMIT 10 OFFSET ?`,
			systemtenant.WorkspaceID,
			cluster,
			capacitymetrics.Pods,
			time.Now().Add(-CapacityLookback),
			offset,
		)
		if err != nil {
			return nil, fmt.Errorf("query capacity manifests: %w", err)
		}
		var candidates []struct {
			sample   CapacitySample
			expected float64
		}
		for rows.Next() {
			var source, interval string
			var at time.Time
			var expected float64
			if err = rows.Scan(&source, &at, &expected, &interval); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			duration, parseErr := time.ParseDuration(interval)
			if parseErr != nil || duration <= 0 {
				return nil, errors.Join(fmt.Errorf("invalid capacity collection interval %q", interval), rows.Close())
			}
			candidates = append(candidates, struct {
				sample   CapacitySample
				expected float64
			}{CapacitySample{cluster, source, at, duration}, expected})
		}
		if err = rows.Err(); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			if candidate.expected < 0 || math.IsNaN(candidate.expected) || math.IsInf(candidate.expected, 0) {
				return nil, errors.New("invalid capacity manifest count")
			}
			complete, checkErr := c.completeCapacity(ctx, candidate.sample, candidate.expected)
			if checkErr != nil {
				return nil, checkErr
			}
			if complete {
				return &candidate.sample, nil
			}
		}
		if len(candidates) < 10 {
			return nil, ErrCapacityUnavailable
		}
	}
}

func (c *Client) completeCapacity(ctx context.Context, s CapacitySample, expected float64) (bool, error) {
	var cpu, memory, pods uint64
	err := c.conn.QueryRow(ctx, `SELECT uniqExactIf(tuple(mapSort(Attributes),Value),MetricName=?),
uniqExactIf(tuple(mapSort(Attributes),Value),MetricName=?),uniqExact(Attributes['pod_uid'])
FROM otel_metrics_gauge WHERE WorkspaceId=? AND ResourceAttributes['loco.cluster.id']=?
AND TimeUnix=fromUnixTimestamp64Milli(?)
AND Attributes['source_id']=? AND MetricName IN (?,?) AND bitAnd(Flags,1)=0`,
		capacitymetrics.CPU, capacitymetrics.Memory,
		systemtenant.WorkspaceID, s.Cluster, s.At.UnixMilli(), s.Source, capacitymetrics.CPU, capacitymetrics.Memory).
		Scan(&cpu, &memory, &pods)
	if err != nil {
		return false, fmt.Errorf("check capacity collection: %w", err)
	}
	return float64(cpu) == expected && float64(memory) == expected && float64(pods) == expected, nil
}

// CapacityTotals are aggregated from actual pod requests in one complete sample.
type CapacityTotals struct {
	Node, Organization, Workspace, Resource string
	CPU, Memory, BuildCPU, BuildMemory      int64
	Pods, Resources                         int32
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
	Group                                      CapacityGroup
}

func (c *Client) CapacityTotals(ctx context.Context, s CapacitySample, scope CapacityScope) ([]CapacityTotals, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	var dimensions string
	switch scope.Group {
	case CapacityByNode:
		dimensions = "node,'' AS organization,'' AS workspace,'' AS resource"
	case CapacityByWorkspace:
		dimensions = "'' AS node,organization,workspace,'' AS resource"
	case CapacityByCluster:
		dimensions = "'' AS node,'' AS organization,'' AS workspace,'' AS resource"
	case CapacityByResource:
		dimensions = "'' AS node,organization,workspace,resource"
	default:
		return nil, errors.New("invalid capacity grouping")
	}
	args := []any{
		systemtenant.WorkspaceID,
		s.Cluster,
		s.At.UnixMilli(),
		s.Source,
		capacitymetrics.CPU,
		capacitymetrics.Memory,
	}
	filters := []string{}
	for _, filter := range []struct{ name, value string }{
		{"node", scope.Node}, {"organization_id", scope.Organization}, {"workspace_id", scope.Workspace},
	} {
		if filter.value != "" {
			filters = append(filters, "Attributes['"+filter.name+"']=?")
			args = append(args, filter.value)
		}
	}
	if len(scope.Resources) > 0 {
		filters = append(filters, "Attributes['resource_id'] IN (?)")
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
 SELECT Attributes['pod_uid'] AS pod,any(Attributes['node']) AS node,any(Attributes['organization_id']) AS organization,
 any(Attributes['workspace_id']) AS workspace,any(Attributes['resource_id']) AS resource,
 any(Attributes['environment_id']) AS environment,any(Attributes['build_id']) AS build,
 maxIf(Value,MetricName='` + capacitymetrics.CPU + `') AS cpu,
 maxIf(Value,MetricName='` + capacitymetrics.Memory + `') AS memory
 FROM otel_metrics_gauge WHERE WorkspaceId=? AND ResourceAttributes['loco.cluster.id']=?
AND TimeUnix=fromUnixTimestamp64Milli(?)
 AND Attributes['source_id']=? AND MetricName IN (?,?) AND bitAnd(Flags,1)=0` + where + ` GROUP BY pod
 ) SELECT ` + dimensions + `,toInt64(round(sumIf(cpu,build='' AND (?='' OR environment=?))*1000)),
 toInt64(sumIf(memory,build='' AND (?='' OR environment=?))),
 toInt64(round(sumIf(cpu,build!='')*1000)),toInt64(sumIf(memory,build!='')),toInt32(count()),
 toInt32(uniqExactIf(pods.resource,pods.resource!='')) FROM pods GROUP BY node,organization,workspace,resource`
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
	Name        string
	CPU, Memory *int64
	Ready       *bool
	At          time.Time
	Stale       bool
}

func (c *Client) NodeCapacities(ctx context.Context, cluster string) ([]NodeCapacity, error) {
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
	rows, err := c.conn.Query(ctx, `SELECT ResourceAttributes['k8s.node.name'],MetricName,
 argMax(Value,TimeUnix),max(TimeUnix),argMax(ResourceAttributes['loco.collection.interval'],TimeUnix)
 FROM otel_metrics_gauge WHERE WorkspaceId=? AND ResourceAttributes['loco.cluster.id']=?
 AND MetricName IN ('k8s.node.allocatable_cpu','k8s.node.allocatable_memory','k8s.node.condition_ready')
 AND bitAnd(Flags,1)=0 AND TimeUnix>=?
GROUP BY ResourceAttributes['k8s.node.name'],MetricName`,
		systemtenant.WorkspaceID, cluster, time.Now().Add(-CapacityLookback))
	if err != nil {
		return nil, fmt.Errorf("query node capacity: %w", err)
	}
	defer rows.Close()
	nodes := map[string]*NodeCapacity{}
	for rows.Next() {
		var name, metric, interval string
		var value float64
		var at time.Time
		if err = rows.Scan(&name, &metric, &value, &at, &interval); err != nil {
			return nil, err
		}
		duration, parseErr := time.ParseDuration(interval)
		if parseErr != nil || duration <= 0 {
			return nil, fmt.Errorf("invalid native node collection interval %q", interval)
		}
		node := nodes[name]
		if node == nil {
			node = &NodeCapacity{Name: name}
			nodes[name] = node
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
	out := make([]NodeCapacity, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, *node)
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
	totals := map[string]*CapacityTotals{}
	samples := []CapacitySample{}
	for _, cluster := range clusters {
		sample, err := c.CapacitySample(ctx, cluster)
		if errors.Is(err, ErrCapacityUnavailable) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		rows, err := c.CapacityTotals(ctx, *sample, scope)
		if err != nil {
			return nil, nil, err
		}
		samples = append(samples, *sample)
		for _, r := range rows {
			key := r.Workspace + ":" + r.Resource
			total := totals[key]
			if total == nil {
				total = &CapacityTotals{
					Node:         r.Node,
					Organization: r.Organization,
					Workspace:    r.Workspace,
					Resource:     r.Resource,
				}
				totals[key] = total
			}
			total.CPU += r.CPU
			total.Memory += r.Memory
			total.BuildCPU += r.BuildCPU
			total.BuildMemory += r.BuildMemory
			total.Pods += r.Pods
		}
	}
	out := make([]CapacityTotals, 0, len(totals))
	for _, r := range totals {
		out = append(out, *r)
	}
	return out, samples, nil
}
