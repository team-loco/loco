package configplan

import (
	"strconv"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestRollbackOnFailureChangeIsPlanned(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		service := fileService()
		service.RollbackOnFailure = new(enabled)
		current := liveState()
		current.RollbackOnFailure = !enabled
		plan := compute(t, input(map[string]locofile.Service{webService: service}, Service{
			Name: webService, Partial: testPartial, Built: true,
			RunningRegions: []string{testRegion}, State: current,
		}))
		op := onlyOperation(t, plan)
		if op.Kind != KindUpdate || op.NeedsDeploy || len(op.Changes) != 1 ||
			op.Changes[0].Path != "rollbackOnFailure" ||
			op.Desired.RollbackOnFailure != enabled ||
			op.Changes[0].Before != strconv.FormatBool(!enabled) ||
			op.Changes[0].After != strconv.FormatBool(enabled) {
			t.Fatalf("operation=%+v", op)
		}
	}
}
