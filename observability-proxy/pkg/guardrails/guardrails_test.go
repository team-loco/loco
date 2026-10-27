package guardrails

import (
	"errors"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	invalidTraceTestUUID = "invalid"
	testWorkspaceID      = "01a11886-e710-7fc9-a6e9-5e0c367f2cd0"
	testMaxRange         = 24 * time.Hour
)

func tailRequest(since *timestamppb.Timestamp) *observabilityv1.TailLogsRequest {
	return &observabilityv1.TailLogsRequest{WorkspaceId: testWorkspaceID, Since: since}
}

func TestValidateTailRequestChecksSince(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: testMaxRange}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	recent := timestamppb.New(now.Add(-time.Minute))
	if err := ValidateTailRequest(tailRequest(recent), cfg, now); err != nil {
		t.Errorf("since a minute ago rejected: %v", err)
	}
	if err := ValidateTailRequest(tailRequest(nil), cfg, now); err != nil {
		t.Errorf("no since rejected: %v", err)
	}

	old := timestamppb.New(now.Add(-testMaxRange - time.Second))
	if err := ValidateTailRequest(tailRequest(old), cfg, now); !errors.Is(err, errSinceTooOld) {
		t.Errorf("since beyond the maximum range = %v, want errSinceTooOld", err)
	}
	future := timestamppb.New(now.Add(time.Minute))
	if err := ValidateTailRequest(tailRequest(future), cfg, now); !errors.Is(err, errSinceInFuture) {
		t.Errorf("since in the future = %v, want errSinceInFuture", err)
	}
	empty := &observabilityv1.TailLogsRequest{}
	if err := ValidateTailRequest(empty, cfg, now); !errors.Is(err, errWorkspaceIDRequired) {
		t.Errorf("no workspace = %v, want errWorkspaceIDRequired", err)
	}
}

func TestLabelFiltersRejectLocoAttributeKeys(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: testMaxRange}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	start := timestamppb.New(now.Add(-time.Hour))
	end := timestamppb.New(now)
	reserved := []string{"loco.workspace.id", "loco.environment.id", "loco.resource.id", "loco.anything"}
	allowed := []string{"loco.io/build-id", "app.loco.io/component", "service.name"}

	for _, key := range reserved {
		labels := map[string]string{key: testWorkspaceID}
		logs := &observabilityv1.QueryLogsRequest{
			WorkspaceId: testWorkspaceID,
			StartTime:   start,
			EndTime:     end,
			Labels:      labels,
		}
		if err := ValidateLogsRequest(logs, cfg); !errors.Is(err, errReservedLabelKey) {
			t.Errorf("logs label %q = %v, want errReservedLabelKey", key, err)
		}
		tail := &observabilityv1.TailLogsRequest{WorkspaceId: testWorkspaceID, Labels: labels}
		if err := ValidateTailRequest(tail, cfg, now); !errors.Is(err, errReservedLabelKey) {
			t.Errorf("tail label %q = %v, want errReservedLabelKey", key, err)
		}
	}
	for _, key := range allowed {
		labels := map[string]string{key: "value"}
		logs := &observabilityv1.QueryLogsRequest{
			WorkspaceId: testWorkspaceID,
			StartTime:   start,
			EndTime:     end,
			Labels:      labels,
		}
		if err := ValidateLogsRequest(logs, cfg); err != nil {
			t.Errorf("logs label %q rejected: %v", key, err)
		}
		tail := &observabilityv1.TailLogsRequest{WorkspaceId: testWorkspaceID, Labels: labels}
		if err := ValidateTailRequest(tail, cfg, now); err != nil {
			t.Errorf("tail label %q rejected: %v", key, err)
		}
	}
}

func TestValidateTracesRequest(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: testMaxRange}
	now := time.Date(2026, time.October, 14, 0, 0, 0, 1, time.UTC)
	cases := []struct {
		name   string
		change func(*observabilityv1.QueryTracesRequest)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "empty workspace", change: func(r *observabilityv1.QueryTracesRequest) { r.WorkspaceId = "" }},
		{
			name:   "invalid workspace",
			change: func(r *observabilityv1.QueryTracesRequest) { r.WorkspaceId = invalidTraceTestUUID },
		},
		{
			name:   "invalid resource",
			change: func(r *observabilityv1.QueryTracesRequest) { r.ResourceIds = []string{invalidTraceTestUUID} },
		},
		{
			name:   "invalid environment",
			change: func(r *observabilityv1.QueryTracesRequest) { r.EnvironmentId = invalidTraceTestUUID },
		},
		{name: "missing start", change: func(r *observabilityv1.QueryTracesRequest) { r.StartTime = nil }},
		{name: "missing end", change: func(r *observabilityv1.QueryTracesRequest) { r.EndTime = nil }},
		{
			name:   "invalid timestamp",
			change: func(r *observabilityv1.QueryTracesRequest) { r.StartTime = &timestamppb.Timestamp{Nanos: -1} },
		},
		{name: "unrepresentable nanoseconds", change: func(r *observabilityv1.QueryTracesRequest) {
			r.StartTime = timestamppb.New(time.Date(2500, time.January, 1, 0, 0, 0, 0, time.UTC))
			r.EndTime = r.GetStartTime()
		}},
		{
			name:   "reversed range",
			change: func(r *observabilityv1.QueryTracesRequest) { r.StartTime = timestamppb.New(now.Add(time.Second)) },
		},
		{name: "excessive range", change: func(r *observabilityv1.QueryTracesRequest) {
			r.StartTime = timestamppb.New(now.Add(-testMaxRange - time.Nanosecond))
		}},
		{
			name:   "exact maximum range",
			valid:  true,
			change: func(r *observabilityv1.QueryTracesRequest) { r.StartTime = timestamppb.New(now.Add(-testMaxRange)) },
		},
		{
			name:   "equal timestamps",
			valid:  true,
			change: func(r *observabilityv1.QueryTracesRequest) { r.StartTime = r.GetEndTime() },
		},
		{
			name:   "known trace",
			valid:  true,
			change: func(r *observabilityv1.QueryTracesRequest) { r.TraceId = "0123456789abcdef0123456789abcdef" },
		},
		{name: "short trace", change: func(r *observabilityv1.QueryTracesRequest) { r.TraceId = "abcd" }},
		{
			name:   "nonhex trace",
			change: func(r *observabilityv1.QueryTracesRequest) { r.TraceId = "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" },
		},
		{
			name:   "uppercase trace",
			change: func(r *observabilityv1.QueryTracesRequest) { r.TraceId = "0123456789ABCDEF0123456789ABCDEF" },
		},
		{
			name:   "zero trace",
			change: func(r *observabilityv1.QueryTracesRequest) { r.TraceId = "00000000000000000000000000000000" },
		},
		{name: "negative limit", change: func(r *observabilityv1.QueryTracesRequest) { r.Limit = -1 }},
		{
			name:   "excessive limit",
			change: func(r *observabilityv1.QueryTracesRequest) { r.Limit = traceRequestMaxLimit + 1 },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &observabilityv1.QueryTracesRequest{
				WorkspaceId: testWorkspaceID,
				StartTime:   timestamppb.New(now.Add(-time.Hour)),
				EndTime:     timestamppb.New(now),
			}
			if tc.change != nil {
				tc.change(req)
			}
			err := ValidateTracesRequest(req, cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestRegionFiltersPreserveReservedLabelGuardrails(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: testMaxRange}
	now := time.Now()
	for _, patterns := range [][]string{
		{""}, {"us**"}, {"us*east"}, {strings.Repeat("x", maxRegionPatternRunes+1)},
		make([]string, maxRegionPatterns+1),
	} {
		logs := &observabilityv1.QueryLogsRequest{WorkspaceId: testWorkspaceID, Regions: patterns,
			StartTime: timestamppb.New(now.Add(-time.Hour)), EndTime: timestamppb.New(now)}
		tail := &observabilityv1.TailLogsRequest{WorkspaceId: testWorkspaceID, ExcludedRegions: patterns}
		if err := ValidateLogsRequest(logs, cfg); !errors.Is(err, errInvalidRegionFilter) {
			t.Errorf("logs: %v", err)
		}
		if err := ValidateTailRequest(tail, cfg, now); !errors.Is(err, errInvalidRegionFilter) {
			t.Errorf("tail: %v", err)
		}
	}
	logs := &observabilityv1.QueryLogsRequest{WorkspaceId: testWorkspaceID, Regions: []string{"us-*"},
		Labels:    map[string]string{"loco.workspace.id": "another workspace"},
		StartTime: timestamppb.New(now.Add(-time.Hour)), EndTime: timestamppb.New(now)}
	if err := ValidateLogsRequest(logs, cfg); !errors.Is(err, errReservedLabelKey) {
		t.Fatalf("reserved label: %v", err)
	}
	logs.Labels = nil
	if err := ValidateLogsRequest(logs, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsHistoryAllowsAllResourcesWithinWorkspace(t *testing.T) {
	now := time.Now()
	request := &observabilityv1.QueryMetricsRequest{WorkspaceId: testWorkspaceID,
		StartTime: timestamppb.New(now.Add(-time.Hour)), EndTime: timestamppb.New(now),
		MetricName: "k8s.pod.cpu.usage", Aggregation: "avg"}
	if err := ValidateMetricsRequest(request, &config.Config{MaxTimeRange: testMaxRange}); err != nil {
		t.Fatal(err)
	}
	request.WorkspaceId = ""
	if err := ValidateMetricsRequest(
		request,
		&config.Config{MaxTimeRange: testMaxRange},
	); !errors.Is(
		err,
		errWorkspaceIDRequired,
	) {
		t.Fatalf("missing workspace: %v", err)
	}
}

func TestLogEnvironmentFilterValidation(t *testing.T) {
	now := time.Now()
	cfg := &config.Config{MaxTimeRange: time.Hour}
	for _, environmentID := range []string{"", "66666666-7777-8888-9999-000000000000", "not-a-uuid"} {
		query := &observabilityv1.QueryLogsRequest{
			WorkspaceId:   "workspace",
			EnvironmentId: environmentID,
			StartTime:     timestamppb.New(now.Add(-time.Minute)),
			EndTime:       timestamppb.New(now),
		}
		tail := &observabilityv1.TailLogsRequest{WorkspaceId: "workspace", EnvironmentId: environmentID}
		for _, err := range []error{ValidateLogsRequest(query, cfg), ValidateTailRequest(tail, cfg, now)} {
			if environmentID == "not-a-uuid" {
				if !errors.Is(err, errInvalidEnvironmentID) {
					t.Fatalf("invalid environment error = %v", err)
				}
			} else if err != nil {
				t.Fatalf("environment %q: %v", environmentID, err)
			}
		}
	}
}

const testWorkspaceAggregation = "max"

func TestEveryTenantQueryRequiresAWorkspace(t *testing.T) {
	cfg := &config.Config{MaxTimeRange: time.Hour}
	now := time.Now()
	start := timestamppb.New(now.Add(-time.Minute))
	end := timestamppb.New(now)
	checks := map[string]error{
		"logs": ValidateLogsRequest(&observabilityv1.QueryLogsRequest{StartTime: start, EndTime: end}, cfg),
		"tail": ValidateTailRequest(&observabilityv1.TailLogsRequest{}, cfg, now),
		"metrics": ValidateMetricsRequest(
			&observabilityv1.QueryMetricsRequest{
				MetricName:  "m",
				Aggregation: testWorkspaceAggregation,
				StartTime:   start,
				EndTime:     end,
			},
			cfg,
		),
		"traces": ValidateTracesRequest(&observabilityv1.QueryTracesRequest{StartTime: start, EndTime: end}, cfg),
	}
	for name, err := range checks {
		if !errors.Is(err, errWorkspaceIDRequired) {
			t.Errorf("%s without a workspace = %v, want errWorkspaceIDRequired", name, err)
		}
	}
}
