package configplan

import (
	"slices"
	"testing"

	"github.com/team-loco/loco/internal/locofile"
)

func TestCommandAndArgsChangesArePlanned(t *testing.T) {
	cases := map[string]struct {
		fileCommand, fileArgs, liveCommand, liveArgs []string
		want                                         []Change
	}{
		"set": {
			fileCommand: []string{"/http-echo"},
			fileArgs:    []string{"-text=hello world", "-listen=:8080"},
			want: []Change{
				{Path: pathArgs, After: `["-text=hello world", "-listen=:8080"]`},
				{Path: pathCommand, After: `["/http-echo"]`},
			},
		},
		"changed": {
			fileArgs: []string{"-text=a, b"},
			liveArgs: []string{"-text=a", "b"},
			want:     []Change{{Path: pathArgs, Before: `["-text=a", "b"]`, After: `["-text=a, b"]`}},
		},
		"removed": {
			liveCommand: []string{"/bin/sh", "-c"},
			want:        []Change{{Path: pathCommand, Before: `["/bin/sh", "-c"]`}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			service := fileService()
			service.Command = tc.fileCommand
			service.Args = tc.fileArgs
			current := liveState()
			current.Command = tc.liveCommand
			current.Args = tc.liveArgs
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
			op := onlyOperation(t, plan)
			if op.Kind != KindUpdate || !slices.Equal(op.Changes, tc.want) {
				t.Fatalf("operation = %+v, want changes %+v", op, tc.want)
			}
			if !slices.Equal(op.Desired.Command, tc.fileCommand) || !slices.Equal(op.Desired.Args, tc.fileArgs) {
				t.Fatalf("desired command %q args %q", op.Desired.Command, op.Desired.Args)
			}
		})
	}
}

func TestCommandRejectsAnEmptyEntryBeforeApply(t *testing.T) {
	service := fileService()
	service.Command = []string{"/bin/sh", ""}
	plan := compute(t, input(map[string]locofile.Service{webService: service}))
	if len(plan.Errors) != 1 || plan.Errors[0].Path != pathCommand || len(plan.Operations) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
}
