package clickhouse

import (
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
	if len(series) != 1 || math.Abs(series[0].GetPoints()[0].GetValue()-10.0/60) > 1e-9 {
		t.Fatalf("restart rate = %v, want ten in-range requests / 60s", series)
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
