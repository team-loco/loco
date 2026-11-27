package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/ext"
	"github.com/ClickHouse/clickhouse-go/v2/lib/column"
	"github.com/team-loco/loco/internal/capacitymetrics"
	"github.com/team-loco/loco/internal/systemtenant"
)

const capacityCandidatePage = 1

//nolint:usestdlibvars // This is a ClickHouse SQL type, not a Go constant kind.
const capacityStringType column.Type = "String"

type capacityCandidate struct {
	CapacitySample
	expected float64
}
type capacityMembership struct{ cpu, memory, pods uint64 }

func capacityCohorts(ctx context.Context, samples []CapacitySample) (context.Context, error) {
	table, err := ext.NewTable(
		"capacity_cohorts",
		ext.Column("Cluster", capacityStringType),
		ext.Column("Source", capacityStringType),
		ext.Column("Captured", "DateTime64(9)"),
	)
	if err != nil {
		return nil, err
	}
	for _, sample := range samples {
		if err = table.Append(sample.Cluster, sample.Source, sample.At); err != nil {
			return nil, err
		}
	}
	return chdriver.Context(ctx, chdriver.WithExternalTable(table)), nil
}

// The source enforces a unique timestamp within its process identity. Repeated
// OTLP delivery therefore repeats the same immutable pod/metric point.
const capacityCohortFilter = `WorkspaceId=? AND bitAnd(Flags,1)=0
AND ClusterId IN (SELECT Cluster FROM capacity_cohorts)
AND TimeUnix IN (SELECT Captured FROM capacity_cohorts)
AND (ClusterId,Attributes['source_id'],TimeUnix)
 IN (SELECT Cluster,Source,Captured FROM capacity_cohorts)`

func (c *Client) capacitySamples(ctx context.Context, clusters []string) ([]CapacitySample, error) {
	pending := clusters
	out := make([]CapacitySample, 0, len(clusters))
	for offset := 0; len(pending) > 0; offset += capacityCandidatePage {
		candidates, counts, err := c.capacityCandidates(ctx, pending, offset)
		if err != nil {
			return nil, err
		}
		snapshots := make([]CapacitySample, 0, len(candidates))
		for _, candidate := range candidates {
			snapshots = append(snapshots, candidate.CapacitySample)
		}
		membership, err := c.capacityMembership(ctx, snapshots)
		if err != nil {
			return nil, err
		}
		found := map[string]bool{}
		for _, candidate := range candidates {
			if found[candidate.Cluster] {
				continue
			}
			members := membership[candidate.Cluster+":"+candidate.ID()]
			complete := float64(members.cpu) == candidate.expected && float64(members.memory) == candidate.expected &&
				float64(members.pods) == candidate.expected
			if complete {
				out = append(out, candidate.CapacitySample)
				found[candidate.Cluster] = true
			}
		}
		next := make([]string, 0, len(pending))
		for _, cluster := range pending {
			if !found[cluster] && counts[cluster] == capacityCandidatePage {
				next = append(next, cluster)
			}
		}
		pending = next
	}
	return out, nil
}

func (c *Client) capacityCandidates(
	ctx context.Context,
	clusters []string,
	offset int,
) ([]capacityCandidate, map[string]int, error) {
	rows, err := c.conn.Query(ctx, `SELECT ClusterId,Attributes['source_id'],TimeUnix,
max(Value),any(ResourceAttributes['loco.collection.interval'])
FROM otel_metrics_gauge WHERE WorkspaceId=? AND has(?,ClusterId)
AND MetricName=? AND bitAnd(Flags,1)=0 AND TimeUnix>=?
GROUP BY ClusterId,Attributes['source_id'],TimeUnix
ORDER BY ClusterId,TimeUnix DESC,Attributes['source_id']
LIMIT ?,? BY ClusterId`,
		systemtenant.WorkspaceID, clusters, capacitymetrics.Pods, time.Now().Add(-CapacityLookback),
		offset, capacityCandidatePage)
	if err != nil {
		return nil, nil, fmt.Errorf("query capacity manifests: %w", err)
	}
	defer rows.Close()
	out := []capacityCandidate{}
	counts := map[string]int{}
	for rows.Next() {
		var candidate capacityCandidate
		var interval string
		if err = rows.Scan(
			&candidate.Cluster,
			&candidate.Source,
			&candidate.At,
			&candidate.expected,
			&interval,
		); err != nil {
			return nil, nil, err
		}
		duration, parseErr := time.ParseDuration(interval)
		if parseErr != nil || duration <= 0 {
			return nil, nil, fmt.Errorf("invalid capacity collection interval %q", interval)
		}
		if !validCapacityCount(candidate.expected) {
			return nil, nil, errors.New("invalid capacity manifest count")
		}
		candidate.Interval = duration
		out = append(out, candidate)
		counts[candidate.Cluster]++
	}
	return out, counts, rows.Err()
}

func validCapacityCount(count float64) bool {
	if math.IsNaN(count) || math.IsInf(count, 0) {
		return false
	}
	return count >= 0 && count == math.Trunc(count)
}

func (c *Client) capacityMembership(
	ctx context.Context,
	samples []CapacitySample,
) (map[string]capacityMembership, error) {
	out := map[string]capacityMembership{}
	if len(samples) == 0 {
		return out, nil
	}
	ctx, err := capacityCohorts(ctx, samples)
	if err != nil {
		return nil, err
	}
	rows, err := c.conn.Query(
		ctx,
		`SELECT ClusterId,Attributes['source_id'],TimeUnix,
uniqExactIf(Attributes['pod_uid'],MetricName=?),
uniqExactIf(Attributes['pod_uid'],MetricName=?),uniqExact(Attributes['pod_uid'])
FROM otel_metrics_gauge WHERE `+capacityCohortFilter+` AND MetricName IN (?,?)
GROUP BY ClusterId,Attributes['source_id'],TimeUnix`,
		capacitymetrics.CPU,
		capacitymetrics.Memory,
		systemtenant.WorkspaceID,
		capacitymetrics.CPU,
		capacitymetrics.Memory,
	)
	if err != nil {
		return nil, fmt.Errorf("check capacity collection membership: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sample CapacitySample
		var counts capacityMembership
		if err = rows.Scan(
			&sample.Cluster,
			&sample.Source,
			&sample.At,
			&counts.cpu,
			&counts.memory,
			&counts.pods,
		); err != nil {
			return nil, err
		}
		out[sample.Cluster+":"+sample.ID()] = counts
	}
	return out, rows.Err()
}
