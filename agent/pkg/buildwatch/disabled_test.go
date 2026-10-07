package buildwatch

import (
	"context"
	"testing"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const disabledTestBuildID = "0199b6c4-5d1e-7f00-8000-000000000001"

func TestDisabledFailsEveryStartBuild(t *testing.T) {
	var reported []*agentv1.BuildStatus
	report := func(status *agentv1.BuildStatus) {
		reported = append(reported, status)
	}
	start := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_StartBuild{
		StartBuild: &agentv1.StartBuild{BuildId: disabledTestBuildID},
	}}
	Disabled{}.Handle(context.Background(), start, report)

	if len(reported) != 1 {
		t.Fatalf("reported %d statuses, want 1", len(reported))
	}
	status := reported[0]
	if status.GetBuildId() != disabledTestBuildID {
		t.Errorf("build id = %q", status.GetBuildId())
	}
	if status.GetPhase() != agentv1.BuildPhase_BUILD_PHASE_FAILED {
		t.Errorf("phase = %v, want failed", status.GetPhase())
	}
	if status.GetMessage() != DisabledMessage {
		t.Errorf("message = %q, want %q", status.GetMessage(), DisabledMessage)
	}
}

func TestDisabledIgnoresCancelsAndReportsNoBuilds(t *testing.T) {
	report := func(status *agentv1.BuildStatus) {
		t.Fatalf("reported %v for a cancel", status)
	}
	cancel := &agentv1.SyncResponse{Message: &agentv1.SyncResponse_CancelBuild{
		CancelBuild: &agentv1.CancelBuild{BuildId: disabledTestBuildID},
	}}
	Disabled{}.Handle(context.Background(), cancel, report)

	err := Disabled{}.WithInventory(context.Background(), func(builds []*agentv1.InventoryBuild) error {
		if len(builds) != 0 {
			t.Errorf("inventory holds %d builds, want none", len(builds))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithInventory: %v", err)
	}
}
