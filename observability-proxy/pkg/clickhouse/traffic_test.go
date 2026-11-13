package clickhouse

import (
	"fmt"
	"math"
	"testing"
	"time"
)

const trafficQuantile = "p95"

func TestTrafficBucketsUseUnixEpochAlignment(t *testing.T) {
	start := time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)
	req := metricsRequest(start, start.Add(10*time.Second))
	req.IntervalSeconds = 7
	buckets := make(map[trafficBucket]map[float64]float64)
	addTrafficSample(buckets, trafficSample{
		resource: testResourceID, stream: "a", start: start, end: start.Add(10 * time.Second),
		temporality: 1, values: []float64{10},
	}, trafficSample{}, req, false)
	series := trafficSeries(buckets, req, false)
	want := time.Unix(start.Unix()/7*7, 0)
	if len(series) != 1 || !series[0].GetPoints()[0].GetTimestamp().AsTime().Equal(want) {
		t.Fatalf("bucket alignment = %v, want first bucket %v", series, want)
	}
	for _, point := range series[0].GetPoints() {
		if math.Abs(point.GetValue()-1) > 1e-9 {
			t.Fatalf("partial bucket rate = %v, want 1", point.GetValue())
		}
	}
}

func TestTrafficCountsRestartBeforeRangeWithKnownPredecessor(t *testing.T) {
	start := time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)
	req := metricsRequest(start, start.Add(time.Minute))
	buckets := make(map[trafficBucket]map[float64]float64)
	previous := trafficSample{
		resource: testResourceID, stream: "a", start: start.Add(-time.Hour), end: start.Add(-20 * time.Second),
		temporality: 2, values: []float64{1000},
	}
	addTrafficSample(buckets, trafficSample{
		resource: testResourceID, stream: "a", start: start.Add(-10 * time.Second), end: start.Add(10 * time.Second),
		temporality: 2, values: []float64{20},
	}, previous, req, false)
	series := trafficSeries(buckets, req, false)
	if len(series) != 1 || math.Abs(series[0].GetPoints()[0].GetValue()-(20.0/3)/60) > 1e-9 {
		t.Fatalf("restart rate = %v, want (20 / 30s) * 10s / 60s", series)
	}
}

func TestTrafficHistogramsMergeAtCommonBounds(t *testing.T) {
	start := time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)
	req := metricsRequest(start, start.Add(time.Minute))
	req.Aggregation = trafficQuantile
	buckets := make(map[trafficBucket]map[float64]float64)
	for _, sample := range []trafficSample{
		{resource: testResourceID, stream: "a", start: start, end: start.Add(time.Minute),
			temporality: 1, values: []float64{0, 10, 0}, bounds: []float64{10, 20}},
		{resource: testResourceID, stream: "b", start: start, end: start.Add(time.Minute),
			temporality: 1, values: []float64{10, 0}, bounds: []float64{20}},
	} {
		addTrafficSample(buckets, sample, trafficSample{}, req, true)
	}
	series := trafficSeries(buckets, req, true)
	if len(series) != 1 || math.Abs(series[0].GetPoints()[0].GetValue()-19) > 1e-9 {
		t.Fatalf("p95 at shared 20ms boundary = %v, want 19ms", series)
	}
}

func TestTrafficHistogramsWithoutSharedFiniteBoundsHaveNoQuantile(t *testing.T) {
	start := time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)
	req := metricsRequest(start, start.Add(time.Minute))
	req.Aggregation = trafficQuantile
	buckets := make(map[trafficBucket]map[float64]float64)
	for _, bound := range []float64{10, 20} {
		addTrafficSample(buckets, trafficSample{
			resource: testResourceID, start: start, end: start.Add(time.Minute),
			temporality: 1, values: []float64{10, 0}, bounds: []float64{bound},
		}, trafficSample{}, req, true)
	}
	if series := trafficSeries(buckets, req, true); len(series) != 0 {
		t.Fatalf("quantile without finite shared bounds = %v, want no estimate", series)
	}
}

func TestTrafficCumulativeRatesIgnoreImplausibleStartTimes(t *testing.T) {
	start := time.Date(2026, time.November, 11, 0, 0, 0, 0, time.UTC)
	for _, changedStart := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed start %t", changedStart), func(t *testing.T) {
			req := metricsRequest(start, start.Add(time.Minute))
			req.IntervalSeconds = 10
			buckets := make(map[trafficBucket]map[float64]float64)
			var previous trafficSample
			for i, value := range []float64{100, 120, 5} {
				accumulationStart := time.Unix(100, 0)
				if changedStart && i == 2 {
					accumulationStart = accumulationStart.Add(time.Second)
				}
				sample := trafficSample{
					resource: testResourceID, stream: "a", start: accumulationStart,
					end: start.Add(time.Duration(i+1) * 10 * time.Second), temporality: 2, values: []float64{value},
				}
				addTrafficSample(buckets, sample, previous, req, false)
				previous = sample
			}
			series := trafficSeries(buckets, req, false)
			if len(series) != 1 || len(series[0].GetPoints()) != 2 {
				t.Fatalf("rates = %v, want two intervals after the baseline", series)
			}
			for i, want := range []float64{2, .5} {
				point := series[0].GetPoints()[i]
				if math.Abs(point.GetValue()-want) > 1e-9 ||
					!point.GetTimestamp().AsTime().Equal(start.Add(time.Duration(i+1)*10*time.Second)) {
					t.Fatalf("point %d = %v, want %v/s over the previous scrape interval", i, point, want)
				}
			}
		})
	}
}
