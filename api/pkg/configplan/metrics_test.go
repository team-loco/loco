package configplan

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestMetricsEndpointChangesArePlanned(t *testing.T) {
	for _, configured := range []bool{true, false} {
		service := fileService()
		current := liveState()
		if configured {
			service.Metrics = &locofile.Metrics{Port: 9090}
		} else {
			current.Metrics = &locofile.Metrics{Port: 9090, Path: "/metrics"}
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
			if change.Path != "metrics.path" && change.Path != "metrics.port" {
				t.Fatalf("unexpected change %+v", change)
			}
		}
		if (op.Desired.Metrics != nil) != configured {
			t.Fatalf("endpoint = %+v", op.Desired.Metrics)
		}
		if configured && op.Desired.Metrics.Path != "/metrics" {
			t.Fatal("missing default metrics path")
		}
	}
}
