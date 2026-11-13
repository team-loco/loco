package clickhouse

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type trafficSample struct {
	resource, stream string
	start, end       time.Time
	temporality      int32
	values, bounds   []float64
}

type trafficBucket struct {
	resource string
	time     time.Time
}

func (c *Client) queryTrafficMetrics(
	ctx context.Context,
	req *observabilityv1.QueryMetricsRequest,
	histogram bool,
) ([]*observabilityv1.MetricSeries, error) {
	filters, args := metricsFilters(req)
	filters = append(filters, "MetricName = ?", fmt.Sprintf("bitAnd(Flags, %d) = 0", noRecordedValueFlag))
	args = append(args, req.GetMetricName())
	table, values, bounds := "otel_metrics_sum", "[Value]", "CAST([], 'Array(Float64)')"
	if histogram {
		table, values, bounds = "otel_metrics_histogram", "arrayMap(x -> toFloat64(x), BucketCounts)", "ExplicitBounds"
	} else {
		filters = append(filters, "IsMonotonic")
	}
	where := strings.Join(filters, " AND ")
	// The predecessor is needed even when the range starts between scrapes.
	columns := fmt.Sprintf(
		"ResourceId, toJSONString(tuple(mapSort(ResourceAttributes), ScopeName, ScopeVersion, "+
			"mapSort(ScopeAttributes), mapSort(Attributes), AggregationTemporality)) AS stream, "+
			"StartTimeUnix, TimeUnix, AggregationTemporality, %s, %s",
		values,
		bounds,
	)
	query := fmt.Sprintf(`SELECT * FROM (
 SELECT %s FROM %s WHERE %s AND TimeUnix >= fromUnixTimestamp64Nano(?) AND TimeUnix <= fromUnixTimestamp64Nano(?)
 UNION ALL
 SELECT * FROM (SELECT %s FROM %s WHERE %s AND TimeUnix < fromUnixTimestamp64Nano(?)
 ORDER BY TimeUnix DESC LIMIT 1 BY stream)
 ) ORDER BY stream, TimeUnix SETTINGS max_execution_time = %d`,
		columns, table, where, columns, table, where, int(c.queryTimeout.Seconds()))
	queryArgs := append(
		slices.Clone(args),
		req.GetStartTime().AsTime().UnixNano(),
		req.GetEndTime().AsTime().UnixNano(),
	)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, req.GetStartTime().AsTime().UnixNano())
	rows, err := c.conn.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query traffic metrics: %w", err)
	}
	defer rows.Close()
	buckets := make(map[trafficBucket]map[float64]float64)
	var previous trafficSample
	for rows.Next() {
		var sample trafficSample
		if err := rows.Scan(
			&sample.resource,
			&sample.stream,
			&sample.start,
			&sample.end,
			&sample.temporality,
			&sample.values,
			&sample.bounds,
		); err != nil {
			return nil, fmt.Errorf("scan traffic metric: %w", err)
		}
		addTrafficSample(buckets, sample, previous, req, histogram)
		previous = sample
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic metrics: %w", err)
	}
	return trafficSeries(buckets, req, histogram), nil
}

func addTrafficSample(
	buckets map[trafficBucket]map[float64]float64,
	sample, previous trafficSample,
	req *observabilityv1.QueryMetricsRequest,
	histogram bool,
) {
	start, end := req.GetStartTime().AsTime(), req.GetEndTime().AsTime()
	if sample.end.Before(start) || !sample.end.After(sample.start) {
		return
	}
	values := slices.Clone(sample.values)
	from := sample.start
	switch sample.temporality {
	case 1:
	case 2:
		// A producer's accumulation start can be invalid; scrape intervals stay usable across resets.
		if previous.stream != sample.stream || !sample.end.After(previous.end) {
			return
		}
		if previous.start.Equal(sample.start) && slices.Equal(previous.bounds, sample.bounds) {
			reset := len(values) != len(previous.values)
			if !reset {
				for i, v := range values {
					if v < previous.values[i] {
						reset = true
						break
					}
				}
			}
			if !reset {
				for i := range values {
					values[i] -= previous.values[i]
				}
			}
		}
		from = previous.end
	default:
		return
	}
	if histogram && len(values) != len(sample.bounds)+1 {
		return
	}
	duration := sample.end.Sub(from).Seconds()
	if duration <= 0 {
		return
	}
	clippedFrom, clippedEnd := from, sample.end
	if clippedFrom.Before(start) {
		clippedFrom = start
	}
	if clippedEnd.After(end) {
		clippedEnd = end
	}
	interval := time.Duration(req.GetIntervalSeconds()) * time.Second
	origin := time.Unix(0, 0)
	first := origin.Add(clippedFrom.Sub(origin).Truncate(interval))
	if first.After(clippedFrom) {
		first = first.Add(-interval)
	}
	for at := first; at.Before(clippedEnd); at = at.Add(interval) {
		left, right := at, at.Add(interval)
		if left.Before(clippedFrom) {
			left = clippedFrom
		}
		if right.After(clippedEnd) {
			right = clippedEnd
		}
		weight := right.Sub(left).Seconds() / duration
		key := trafficBucket{resource: sample.resource, time: at}
		counts := buckets[key]
		if counts == nil {
			counts = make(map[float64]float64)
			buckets[key] = counts
		}
		if histogram {
			addHistogramCounts(counts, sample.bounds, values, weight)
		} else {
			counts[math.Inf(1)] += values[0] * weight
		}
	}
}

func trafficSeries(
	buckets map[trafficBucket]map[float64]float64,
	req *observabilityv1.QueryMetricsRequest,
	histogram bool,
) []*observabilityv1.MetricSeries {
	keys := make([]trafficBucket, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b trafficBucket) int {
		if n := strings.Compare(a.resource, b.resource); n != 0 {
			return n
		}
		return a.time.Compare(b.time)
	})
	var result []*observabilityv1.MetricSeries
	for _, key := range keys {
		value := buckets[key][math.Inf(1)]
		if histogram {
			quantile := map[string]float64{"p50": .5, "p95": .95, "p99": .99}[req.GetAggregation()]
			value = histogramQuantile(buckets[key], quantile)
			if math.IsNaN(value) {
				continue
			}
		} else {
			from, to := key.time, key.time.Add(time.Duration(req.GetIntervalSeconds())*time.Second)
			if start := req.GetStartTime().AsTime(); from.Before(start) {
				from = start
			}
			if end := req.GetEndTime().AsTime(); to.After(end) {
				to = end
			}
			value /= to.Sub(from).Seconds()
		}
		if len(result) == 0 || result[len(result)-1].GetResourceId() != key.resource {
			result = append(result, &observabilityv1.MetricSeries{ResourceId: key.resource})
		}
		series := result[len(result)-1]
		series.Points = append(
			series.Points,
			&observabilityv1.MetricPoint{Timestamp: timestamppb.New(key.time), Value: value},
		)
	}
	return result
}

func addHistogramCounts(counts map[float64]float64, bounds, values []float64, weight float64) {
	common := append(slices.Clone(bounds), math.Inf(1))
	if len(counts) > 0 {
		// Only shared boundaries preserve whole buckets when replicas use different layouts.
		common = slices.DeleteFunc(common, func(bound float64) bool { _, ok := counts[bound]; return !ok })
		for bound, count := range counts {
			index := sort.SearchFloat64s(common, bound)
			if common[index] != bound {
				counts[common[index]] += count
				delete(counts, bound)
			}
		}
	}
	for i, value := range values {
		upper := math.Inf(1)
		if i < len(bounds) {
			upper = bounds[i]
		}
		counts[common[sort.SearchFloat64s(common, upper)]] += value * weight
	}
}

func histogramQuantile(counts map[float64]float64, quantile float64) float64 {
	bounds := make([]float64, 0, len(counts))
	total := 0.0
	for bound, count := range counts {
		bounds = append(bounds, bound)
		total += count
	}
	if total == 0 || len(bounds) < 2 {
		return math.NaN()
	}
	slices.Sort(bounds)
	rank, cumulative, lower := total*quantile, 0.0, 0.0
	for _, upper := range bounds {
		count := counts[upper]
		if cumulative+count >= rank {
			if math.IsInf(upper, 1) {
				return lower
			}
			if upper <= 0 {
				lower = upper
			}
			return lower + (upper-lower)*(rank-cumulative)/count
		}
		cumulative += count
		lower = upper
	}
	return math.NaN()
}
