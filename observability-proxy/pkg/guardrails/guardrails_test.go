package guardrails

import (
	"errors"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testWorkspaceID = "01a11886-e710-7fc9-a6e9-5e0c367f2cd0"
	testMaxRange    = 24 * time.Hour
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
