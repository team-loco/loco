package buildwatch

import (
	"context"
	"log/slog"
	"time"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const DisabledMessage = "builds are not enabled on this cluster: " +
	"the loco-operator chart was installed with builds.enabled=false"

type Disabled struct{}

func (Disabled) Attach(Sink) func() {
	return func() {}
}

func (Disabled) WithInventory(_ context.Context, fn func([]*agentv1.InventoryBuild) error) error {
	return fn(nil)
}

func (Disabled) RunCollector(context.Context, time.Duration) {}

func (Disabled) Handle(ctx context.Context, msg *agentv1.SyncResponse, report Sink) {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SyncResponse_StartBuild:
		buildID := m.StartBuild.GetBuildId()
		slog.WarnContext(ctx, "refusing a build on a cluster without builds", "build_id", buildID)
		report(&agentv1.BuildStatus{
			BuildId: buildID,
			Phase:   agentv1.BuildPhase_BUILD_PHASE_FAILED,
			Message: DisabledMessage,
		})
	case *agentv1.SyncResponse_CancelBuild:
		slog.InfoContext(ctx, "ignoring a build cancel on a cluster without builds",
			"build_id", m.CancelBuild.GetBuildId(),
		)
	default:
		slog.WarnContext(ctx, "ignoring non-build sync message")
	}
}
