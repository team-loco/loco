package configplan

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestInjectionChangeIsPlanned(t *testing.T) {
	service := fileService()
	service.Observability = &locofile.Observability{InjectOtel: new(false)}
	plan := compute(t, input(map[string]locofile.Service{webService: service}, Service{
		Name: webService, Partial: testPartial, Built: true,
		RunningRegions: []string{testRegion}, State: liveState(),
	}))
	op := onlyOperation(t, plan)
	if op.Kind != KindUpdate || len(op.Changes) != 1 || op.Changes[0].Path != "observability.injectOtel" ||
		op.Changes[0].Before != "true" ||
		op.Changes[0].After != "false" {
		t.Fatalf("operation=%+v", op)
	}
}
