package guardrails

import (
	"errors"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	testWorkspaceID          = "01a11886-e710-7fc9-a6e9-5e0c367f2cd0"
	testEnvironmentID        = "66666666-7777-8888-9999-000000000000"
	testMaxRange             = 24 * time.Hour
	testWorkspaceAggregation = "max"
	testTraceLimit           = 10000
	malformedID              = "invalid"
)

var errProtoRule = errors.New("proto rule")

// validated mirrors the query server: the validation interceptor checks the proto rules
// before a handler runs the guardrail.
func validated(req proto.Message, guardrail func() error) error {
	if err := protovalidate.Validate(req); err != nil {
		return errors.Join(errProtoRule, err)
	}
	return guardrail()
}

func tailRequest(now time.Time, change func(*observabilityv1.TailLogsRequest)) error {
	req := &observabilityv1.TailLogsRequest{WorkspaceId: testWorkspaceID}
	change(req)
	return validated(
		req,
		func() error { return ValidateTailRequest(req, Limits{MaxTimeRange: testMaxRange}, now) },
	)
}

func TestValidateTailRequestChecksSince(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	since := func(at *timestamppb.Timestamp) func(*observabilityv1.TailLogsRequest) {
		return func(r *observabilityv1.TailLogsRequest) { r.Since = at }
	}

	if err := tailRequest(now, since(timestamppb.New(now.Add(-time.Minute)))); err != nil {
		t.Errorf("since a minute ago rejected: %v", err)
	}
	if err := tailRequest(now, since(nil)); err != nil {
		t.Errorf("no since rejected: %v", err)
	}
	old := timestamppb.New(now.Add(-testMaxRange - time.Second))
	if err := tailRequest(now, since(old)); !errors.Is(err, errSinceTooOld) {
		t.Errorf("since beyond the maximum range = %v, want errSinceTooOld", err)
	}
	future := timestamppb.New(now.Add(time.Minute))
	if err := tailRequest(now, since(future)); !errors.Is(err, errSinceInFuture) {
		t.Errorf("since in the future = %v, want errSinceInFuture", err)
	}
}

func TestValidateListTracesRequest(t *testing.T) {
	cfg := Limits{MaxTimeRange: testMaxRange}
	now := time.Date(2026, time.October, 14, 0, 0, 0, 1, time.UTC)
	cases := []struct {
		name   string
		change func(*observabilityv1.ListTracesRequest)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "empty workspace", change: func(r *observabilityv1.ListTracesRequest) { r.Scope.WorkspaceId = "" }},
		{
			name:   "invalid workspace",
			change: func(r *observabilityv1.ListTracesRequest) { r.Scope.WorkspaceId = malformedID },
		},
		{
			name:   "invalid resource",
			change: func(r *observabilityv1.ListTracesRequest) { r.Scope.ResourceIds = []string{malformedID} },
		},
		{
			name:   "invalid environment",
			change: func(r *observabilityv1.ListTracesRequest) { r.Scope.EnvironmentId = malformedID },
		},
		{name: "missing start", change: func(r *observabilityv1.ListTracesRequest) { r.StartTime = nil }},
		{name: "missing end", change: func(r *observabilityv1.ListTracesRequest) { r.EndTime = nil }},
		{
			name:   "invalid timestamp",
			change: func(r *observabilityv1.ListTracesRequest) { r.StartTime = &timestamppb.Timestamp{Nanos: -1} },
		},
		{name: "unrepresentable nanoseconds", change: func(r *observabilityv1.ListTracesRequest) {
			r.StartTime = timestamppb.New(time.Date(2500, time.January, 1, 0, 0, 0, 0, time.UTC))
			r.EndTime = r.GetStartTime()
		}},
		{
			name:   "reversed range",
			change: func(r *observabilityv1.ListTracesRequest) { r.StartTime = timestamppb.New(now.Add(time.Second)) },
		},
		{name: "excessive range", change: func(r *observabilityv1.ListTracesRequest) {
			r.StartTime = timestamppb.New(now.Add(-testMaxRange - time.Nanosecond))
		}},
		{
			name:   "exact maximum range",
			valid:  true,
			change: func(r *observabilityv1.ListTracesRequest) { r.StartTime = timestamppb.New(now.Add(-testMaxRange)) },
		},
		{
			name:   "equal timestamps",
			valid:  true,
			change: func(r *observabilityv1.ListTracesRequest) { r.StartTime = r.GetEndTime() },
		},
		{
			name:   "known trace",
			valid:  true,
			change: func(r *observabilityv1.ListTracesRequest) { r.TraceId = "0123456789abcdef0123456789abcdef" },
		},
		{name: "short trace", change: func(r *observabilityv1.ListTracesRequest) { r.TraceId = "abcd" }},
		{
			name:   "nonhex trace",
			change: func(r *observabilityv1.ListTracesRequest) { r.TraceId = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" },
		},
		{
			name:   "uppercase trace",
			change: func(r *observabilityv1.ListTracesRequest) { r.TraceId = "0123456789ABCDEF0123456789ABCDEF" },
		},
		{
			name:   "zero trace",
			change: func(r *observabilityv1.ListTracesRequest) { r.TraceId = "00000000000000000000000000000000" },
		},
		{name: "negative limit", change: func(r *observabilityv1.ListTracesRequest) { r.Limit = -1 }},
		{name: "excessive limit", change: func(r *observabilityv1.ListTracesRequest) { r.Limit = testTraceLimit + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &observabilityv1.ListTracesRequest{
				Scope:     &observabilityv1.TraceScope{WorkspaceId: testWorkspaceID},
				StartTime: timestamppb.New(now.Add(-time.Hour)),
				EndTime:   timestamppb.New(now),
			}
			if tc.change != nil {
				tc.change(req)
			}
			err := validated(req, func() error { return ValidateListTracesRequest(req, cfg) })
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func validateEveryQuery(workspace, environment string, resources []string) map[string]error {
	cfg := Limits{MaxTimeRange: time.Hour}
	now := time.Now()
	start := timestamppb.New(now.Add(-time.Minute))
	end := timestamppb.New(now)
	logs := &observabilityv1.QueryLogsRequest{
		WorkspaceId: workspace, EnvironmentId: environment, ResourceIds: resources, StartTime: start, EndTime: end,
	}
	tail := &observabilityv1.TailLogsRequest{WorkspaceId: workspace, EnvironmentId: environment, ResourceIds: resources}
	metrics := &observabilityv1.QueryMetricsRequest{
		WorkspaceId: workspace, EnvironmentId: environment, ResourceIds: resources, StartTime: start, EndTime: end,
		MetricName: "m", IntervalSeconds: 1, Aggregation: testWorkspaceAggregation,
	}
	traces := &observabilityv1.ListTracesRequest{
		Scope: &observabilityv1.TraceScope{
			WorkspaceId:   workspace,
			EnvironmentId: environment,
			ResourceIds:   resources,
		},
		StartTime: start,
		EndTime:   end,
	}
	getTrace := &observabilityv1.GetTraceRequest{Scope: traces.GetScope(), TraceId: "0123456789abcdef0123456789abcdef"}
	return map[string]error{
		"logs":      validated(logs, func() error { return ValidateLogsRequest(logs, cfg) }),
		"tail":      validated(tail, func() error { return ValidateTailRequest(tail, cfg, now) }),
		"metrics":   validated(metrics, func() error { return ValidateMetricsRequest(metrics, cfg) }),
		"traces":    validated(traces, func() error { return ValidateListTracesRequest(traces, cfg) }),
		"get trace": validated(getTrace, func() error { return ValidateTraceScope(getTrace.GetScope()) }),
	}
}

func TestEveryTenantQueryRequiresCanonicalUUIDScope(t *testing.T) {
	uppercase := strings.ToUpper(testWorkspaceID)
	scopes := map[string]struct {
		workspace, environment string
		resources              []string
		want                   error
	}{
		"valid": {
			workspace:   testWorkspaceID,
			environment: testEnvironmentID,
			resources:   []string{testWorkspaceID},
		},
		"missing workspace":     {want: errProtoRule},
		"non-UUID workspace":    {workspace: "workspace", want: errProtoRule},
		"non-UUID environment":  {workspace: testWorkspaceID, environment: "environment", want: errProtoRule},
		"non-UUID resource":     {workspace: testWorkspaceID, resources: []string{"resource"}, want: errProtoRule},
		"braced workspace":      {workspace: "{" + testWorkspaceID + "}", want: errProtoRule},
		"uppercase workspace":   {workspace: uppercase, want: errNonCanonicalID},
		"uppercase environment": {workspace: testWorkspaceID, environment: uppercase, want: errNonCanonicalID},
		"uppercase resource":    {workspace: testWorkspaceID, resources: []string{uppercase}, want: errNonCanonicalID},
	}
	for name, scope := range scopes {
		for request, err := range validateEveryQuery(scope.workspace, scope.environment, scope.resources) {
			if scope.want == nil && err != nil || scope.want != nil && !errors.Is(err, scope.want) {
				t.Errorf("%s with %s = %v, want %v", request, name, err, scope.want)
			}
		}
	}
}
