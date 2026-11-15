package clickhouse

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

// bucketCounts is one bucket's trace count, error count, p50 and p95.
type bucketCounts [4]uint64

func countsOf(bucket *observabilityv1.TraceBucket) bucketCounts {
	return bucketCounts{
		bucket.GetTraceCount(), bucket.GetErrorCount(), bucket.GetP50DurationNanos(), bucket.GetP95DurationNanos(),
	}
}

func TestTraceBuckets(t *testing.T) {
	f, summaries := newTraceSummaryFixture(t)
	base := &observabilityv1.QueryTraceBucketsRequest{
		Scope:           summaries.GetScope(),
		StartTime:       summaries.GetStartTime(),
		EndTime:         summaries.GetEndTime(),
		IntervalSeconds: 1,
	}
	empty := bucketCounts{}
	cart := bucketCounts{1, 1, uint64(4 * time.Second), uint64(4 * time.Second)}
	t.Run("total", func(t *testing.T) {
		result, err := f.client.QueryTraceBuckets(t.Context(), base)
		if err != nil {
			t.Fatal(err)
		}
		want := bucketCounts{3, 1, uint64(time.Second), uint64(3700 * time.Millisecond)}
		if got := countsOf(result.GetTotal()); got != want {
			t.Fatalf("total = %v, want %v", got, want)
		}
		req := proto.CloneOf(base)
		req.Scope.WorkspaceId = testUnknownWorkspaceID
		result, err = f.client.QueryTraceBuckets(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got := countsOf(result.GetTotal()); got != (bucketCounts{}) {
			t.Fatalf("empty total = %v", got)
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*observabilityv1.QueryTraceBucketsRequest)
		want   []bucketCounts
	}{
		{"one second", func(*observabilityv1.QueryTraceBucketsRequest) {}, []bucketCounts{
			cart, {2, 0, uint64(time.Second), uint64(time.Second)}, empty, empty, empty, empty,
		}},
		{"percentiles", func(r *observabilityv1.QueryTraceBucketsRequest) { r.IntervalSeconds = 5 }, []bucketCounts{
			{3, 1, uint64(time.Second), uint64(3700 * time.Millisecond)}, empty,
		}},
		{"unaligned start", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.StartTime = timestamppb.New(r.GetStartTime().AsTime().Add(1500 * time.Millisecond))
			r.EndTime = timestamppb.New(r.GetStartTime().AsTime().Add(time.Second))
			r.IntervalSeconds = 2
		}, []bucketCounts{empty, empty}},
		{"status", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.Status = observabilityv1.TraceStatus_TRACE_STATUS_OK
			r.IntervalSeconds = 5
		}, []bucketCounts{{2, 0, uint64(time.Second), uint64(time.Second)}, empty}},
		{"root name terms", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.RootNameTerms = []string{"cart"}
			r.IntervalSeconds = 5
		}, []bucketCounts{{2, 1, uint64(2500 * time.Millisecond), uint64(3850 * time.Millisecond)}, empty}},
		{"minimum duration", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.MinDurationNanos = proto.Uint64(uint64(2 * time.Second))
		}, []bucketCounts{cart, empty, empty, empty, empty, empty}},
		{"maximum duration", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.MaxDurationNanos = proto.Uint64(uint64(2 * time.Second))
			r.IntervalSeconds = 5
		}, []bucketCounts{{2, 0, uint64(time.Second), uint64(time.Second)}, empty}},
		{"trace id", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.TraceId = fmt.Sprintf("%032x", 1)
		}, []bucketCounts{cart, empty, empty, empty, empty, empty}},
		{"resource scope", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.Scope = &observabilityv1.TraceScope{
				WorkspaceId:   testWorkspaceID,
				EnvironmentId: testEnvironmentID,
				ResourceIds:   []string{testWorkerResourceID},
			}
			r.IntervalSeconds = 5
		}, []bucketCounts{{1, 1, uint64(3 * time.Second), uint64(3 * time.Second)}, empty}},
		{"workspace scope", func(r *observabilityv1.QueryTraceBucketsRequest) {
			r.Scope = &observabilityv1.TraceScope{WorkspaceId: testOtherWorkspaceID}
			r.IntervalSeconds = 5
		}, []bucketCounts{{1, 0, uint64(time.Hour), uint64(time.Hour)}, empty}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := proto.CloneOf(base)
			tc.change(req)
			result, err := f.client.QueryTraceBuckets(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			interval := time.Duration(req.GetIntervalSeconds()) * time.Second
			got := make([]bucketCounts, 0, len(result.GetBuckets()))
			for i, bucket := range result.GetBuckets() {
				start := req.GetStartTime().AsTime().Truncate(interval).Add(time.Duration(i) * interval)
				if !bucket.GetStartTime().AsTime().Equal(start) {
					t.Fatalf("bucket %d starts at %v, want %v", i, bucket.GetStartTime().AsTime(), start)
				}
				got = append(got, countsOf(bucket))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("buckets = %v, want %v", got, tc.want)
			}
		})
	}
}
