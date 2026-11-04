package configplan

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

const (
	deployAttemptsPath   = pathDeploy + ".attempts"
	deployRetryDelayPath = pathDeploy + ".retryDelay"
)

func TestDeployPolicyChangesArePlanned(t *testing.T) {
	cases := map[string]struct {
		file *locofile.Deploy
		live *locofile.Deploy
		want map[string]Change
	}{
		"set": {
			file: &locofile.Deploy{Attempts: new(int32(4)), RetryDelay: new(int32(60))},
			want: map[string]Change{
				deployAttemptsPath:   {Path: deployAttemptsPath, After: "4"},
				deployRetryDelayPath: {Path: deployRetryDelayPath, After: "60"},
			},
		},
		"removed": {
			live: &locofile.Deploy{Attempts: new(int32(4))},
			want: map[string]Change{
				deployAttemptsPath: {Path: deployAttemptsPath, Before: "4"},
			},
		},
		"unchanged": {
			file: &locofile.Deploy{Attempts: new(int32(4))},
			live: &locofile.Deploy{Attempts: new(int32(4))},
			want: map[string]Change{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service := fileService()
			service.Deploy = tc.file
			current := liveState()
			current.Deploy = tc.live
			plan := compute(t, input(
				map[string]locofile.Service{webService: service},
				Service{
					Name:           webService,
					Partial:        testPartial,
					Built:          true,
					RunningRegions: []string{testRegion},
					State:          current,
				},
			))
			if len(tc.want) == 0 {
				if len(plan.Operations) != 0 {
					t.Fatalf("operations = %+v, want none", plan.Operations)
				}
				return
			}
			op := onlyOperation(t, plan)
			if op.Kind != KindUpdate || op.NeedsDeploy || len(op.Changes) != len(tc.want) {
				t.Fatalf("operation = %+v", op)
			}
			for _, change := range op.Changes {
				if tc.want[change.Path] != change {
					t.Errorf("change = %+v, want %+v", change, tc.want[change.Path])
				}
			}
		})
	}
}
