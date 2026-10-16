package guardrails

import (
	"math"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHistoryQueriesValidateTimestampPresenceAndPrecision(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: testMaxRange}
	checks := []struct {
		name     string
		validate func(start, end *timestamppb.Timestamp) error
	}{
		{name: "logs", validate: func(start, end *timestamppb.Timestamp) error {
			return ValidateLogsRequest(&observabilityv1.QueryLogsRequest{
				WorkspaceId: testWorkspaceID, StartTime: start, EndTime: end,
			}, cfg)
		}},
		{name: "metrics", validate: func(start, end *timestamppb.Timestamp) error {
			return ValidateMetricsRequest(&observabilityv1.QueryMetricsRequest{
				WorkspaceId: testWorkspaceID, StartTime: start, EndTime: end,
				MetricName: "dogfood.time.range", Aggregation: "avg",
			}, cfg)
		}},
		{name: "traces", validate: func(start, end *timestamppb.Timestamp) error {
			return ValidateTracesRequest(&observabilityv1.QueryTracesRequest{
				WorkspaceId: testWorkspaceID, StartTime: start, EndTime: end,
			}, cfg)
		}},
	}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	epoch := timestamppb.New(time.Unix(0, 0))
	minimum := time.Unix(0, math.MinInt64)
	maximum := time.Unix(0, math.MaxInt64)
	tooEarly := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	tooLate := time.Date(2611, 5, 7, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		start, end *timestamppb.Timestamp
		valid      bool
	}{
		{name: "both omitted"},
		{name: "start omitted", end: epoch},
		{name: "end omitted", start: epoch},
		{name: "explicit epoch", start: epoch, end: epoch, valid: true},
		{name: "recent", start: timestamppb.New(now.Add(-time.Minute)), end: timestamppb.New(now), valid: true},
		{name: "equal times", start: timestamppb.New(now), end: timestamppb.New(now), valid: true},
		{
			name:  "maximum duration",
			start: timestamppb.New(now.Add(-testMaxRange)), end: timestamppb.New(now), valid: true,
		},
		{
			name:  "duration exceeded",
			start: timestamppb.New(now.Add(-testMaxRange - time.Nanosecond)), end: timestamppb.New(now),
		},
		{name: "reversed", start: timestamppb.New(now), end: timestamppb.New(now.Add(-time.Minute))},
		{name: "minimum nanos", start: timestamppb.New(minimum), end: timestamppb.New(minimum), valid: true},
		{name: "maximum nanos", start: timestamppb.New(maximum), end: timestamppb.New(maximum), valid: true},
		{name: "below minimum", start: timestamppb.New(minimum.Add(-time.Nanosecond)), end: timestamppb.New(minimum)},
		{name: "above maximum", start: timestamppb.New(maximum), end: timestamppb.New(maximum.Add(time.Nanosecond))},
		{name: "ancient range", start: timestamppb.New(tooEarly), end: timestamppb.New(tooEarly.Add(time.Minute))},
		{name: "future overflow", start: timestamppb.New(tooLate), end: timestamppb.New(tooLate.Add(time.Minute))},
		{
			name:  "invalid nanoseconds",
			start: &timestamppb.Timestamp{Seconds: now.Unix(), Nanos: int32(time.Second)},
			end:   timestamppb.New(now.Add(time.Minute)),
		},
		{
			name:  "invalid protobuf date",
			start: timestamppb.New(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)),
			end:   timestamppb.New(time.Date(10000, 1, 1, 0, 1, 0, 0, time.UTC)),
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					err := check.validate(tc.start, tc.end)
					if (err == nil) != tc.valid {
						t.Fatalf("valid=%v, error=%v", tc.valid, err)
					}
				})
			}
		})
	}
}
