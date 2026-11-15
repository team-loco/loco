package configplan

import (
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestFileChangesArePlannedWithoutTheirContent(t *testing.T) {
	const path = "/otel-config.yml"
	service := fileService()
	service.Files = map[string]string{path: "file_format: \"1.1\"\n"}
	current := liveState()
	current.Files = map[string]string{path: "file_format: \"1.0\"\n"}
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
	if op.Kind != KindUpdate || len(op.Changes) != 1 || op.Changes[0].Path != pathFiles+"."+path {
		t.Fatalf("operation = %+v", op)
	}
	change := op.Changes[0]
	if change.Before == change.After || strings.Contains(change.After, "file_format") {
		t.Fatalf("change = %+v", change)
	}
	if op.Desired.Files[path] != service.Files[path] {
		t.Fatalf("desired files = %v", op.Desired.Files)
	}
}

func TestFilesRejectAReservedPathBeforeApply(t *testing.T) {
	service := fileService()
	service.Files = map[string]string{"/etc/resolv.conf": "nameserver 10.0.0.1\n"}
	plan := compute(t, input(map[string]locofile.Service{webService: service}))
	if len(plan.Errors) != 1 || plan.Errors[0].Path != pathFiles || len(plan.Operations) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}
