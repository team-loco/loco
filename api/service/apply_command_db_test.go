package service

import (
	"slices"
	"strings"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
)

func TestApplyCommandAndArgsPersistAndClear(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	withCommand := strings.Replace(
		base,
		"    port: 8080",
		"    port: 8080\n    command: [/http-echo]\n    args: [-listen=:8080, \"-text=hello world\"]",
		1,
	)
	applyOK(t, f, base, applyOptions{})
	for _, tc := range []struct {
		name          string
		file          string
		command, args []string
		wantPaths     []string
	}{
		{
			name:      "command and args",
			file:      withCommand,
			command:   []string{"/http-echo"},
			args:      []string{"-listen=:8080", "-text=hello world"},
			wantPaths: []string{"args", "command"},
		},
		{name: "cleared", file: base, wantPaths: []string{"args", "command"}},
	} {
		f.finishRollouts(t)
		planned, err := plan(t, f, tc.file, f.workspaceReadScopes(t))
		if err != nil {
			t.Fatalf("%s: plan: %v", tc.name, err)
		}
		var paths []string
		for _, op := range planned.GetOperations() {
			for _, change := range op.GetChanges() {
				paths = append(paths, change.GetPath())
			}
		}
		if !slices.Equal(paths, tc.wantPaths) {
			t.Fatalf("%s: planned changes %q, want %q", tc.name, paths, tc.wantPaths)
		}
		before := f.workerDeployments(t)[0].ID
		applyOK(t, f, tc.file, applyOptions{})
		wantCleanPlan(t, f, tc.file)
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 || deployments[0].ID == before {
			t.Fatalf("%s: active deployments = %v, want one new deployment", tc.name, deployments)
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		service := stored.GetService()
		if !slices.Equal(service.GetCommand(), tc.command) || !slices.Equal(service.GetArgs(), tc.args) {
			t.Fatalf("%s: snapshot command %q args %q", tc.name, service.GetCommand(), service.GetArgs())
		}
		deployment := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Deployment
		if !slices.Equal(deployment.Command, tc.command) || !slices.Equal(deployment.Args, tc.args) {
			t.Fatalf("%s: placement command %q args %q", tc.name, deployment.Command, deployment.Args)
		}
	}
}
