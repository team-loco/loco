package configplan

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestObservabilityChangesArePlannedIndependently(t *testing.T) {
	for _, field := range []string{"logs", "metrics", "traces", "autoinjectOtelEnvVars"} {
		t.Run(field, func(t *testing.T) {
			service := fileService()
			service.Observability = &locofile.Observability{}
			switch field {
			case "logs":
				service.Observability.Logs = new(false)
			case "metrics":
				service.Observability.Metrics = new(false)
			case "traces":
				service.Observability.Traces = new(false)
			case "autoinjectOtelEnvVars":
				service.Observability.AutoinjectOtelEnvVars = new(true)
			}
			plan := compute(t, input(map[string]locofile.Service{webService: service}, Service{
				Name:           webService,
				Partial:        testPartial,
				Built:          true,
				RunningRegions: []string{testRegion},
				State:          liveState(),
			}))
			op := onlyOperation(t, plan)
			if op.Kind != KindUpdate || len(op.Changes) != 1 || op.Changes[0].Path != "observability."+field {
				t.Fatalf("operation=%+v", op)
			}
		})
	}
}
