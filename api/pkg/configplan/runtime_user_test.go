package configplan

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestRuntimeUserChangesArePlanned(t *testing.T) {
	for _, id := range []*int64{new(int64(0)), new(int64(1654)), nil} {
		configured := id != nil
		service := fileService()
		current := liveState()
		if configured {
			service.RunAsUser = id
			service.RunAsGroup = id
		} else {
			current.RunAsUser = new(int64(1654))
			current.RunAsGroup = new(int64(1654))
		}
		plan := compute(
			t,
			input(
				map[string]locofile.Service{webService: service},
				Service{
					Name:           webService,
					Partial:        testPartial,
					Built:          true,
					RunningRegions: []string{testRegion},
					State:          current,
				},
			),
		)
		op := onlyOperation(t, plan)
		if op.Kind != KindUpdate || len(op.Changes) != 2 {
			t.Fatalf("operation = %+v", op)
		}
		for _, change := range op.Changes {
			if change.Path != "runAsUser" && change.Path != "runAsGroup" {
				t.Errorf("unexpected change: %+v", change)
			}
		}
		if (op.Desired.RunAsUser != nil) != configured {
			t.Fatalf("runtime UID = %v", op.Desired.RunAsUser)
		}
	}
}

func TestRuntimeUserRejectsUnmappedIDsBeforeApply(t *testing.T) {
	for _, id := range []int64{-1, 65536} {
		service := fileService()
		service.RunAsUser = &id
		plan := compute(t, input(map[string]locofile.Service{webService: service}))
		if len(plan.Errors) != 1 || plan.Errors[0].Path != "runAsUser" || len(plan.Operations) != 0 {
			t.Fatalf("ID %d plan = %+v", id, plan)
		}
	}
}
