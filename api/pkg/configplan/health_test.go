package configplan

import (
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestOmittedHealthDoesNotCreateHTTPCheck(t *testing.T) {
	service := fileService()
	state := fileState(service, "", testDefaults())
	if state.Health != nil {
		t.Fatalf("omitted health = %+v, want absent", state.Health)
	}
	for path := range flatten(&state) {
		if strings.HasPrefix(path, "health.") {
			t.Errorf("omitted health produced plan field %q", path)
		}
	}
}

func TestConfiguredHealthUsesDefaults(t *testing.T) {
	service := fileService()
	service.Health = &locofile.Health{}
	state := fileState(service, "", testDefaults())
	if state.Health == nil || *state.Health != DefaultHealth(testDefaults()) {
		t.Fatalf("configured health = %+v, want HTTP defaults", state.Health)
	}
}

func TestRemovingHealthPlansUpdate(t *testing.T) {
	live := liveState()
	health := DefaultHealth(testDefaults())
	live.Health = &health
	existing := Service{
		Name:           webService,
		Partial:        testPartial,
		Built:          true,
		RunningRegions: []string{testRegion},
		State:          live,
	}
	op := onlyOperation(t, compute(t, input(map[string]locofile.Service{webService: fileService()}, existing)))
	if op.Kind != KindUpdate || op.Desired.Health != nil {
		t.Fatalf("removing health = %+v, want redeploy without health", op)
	}
	if len(op.Changes) != 5 {
		t.Fatalf("changes = %+v, want the five removed health fields", op.Changes)
	}
	for _, change := range op.Changes {
		if !strings.HasPrefix(change.Path, "health.") || change.After != "" {
			t.Errorf("change = %+v, want removed health field", change)
		}
	}
}
