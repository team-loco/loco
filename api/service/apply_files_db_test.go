package service

import (
	"maps"
	"strings"
	"testing"
)

func TestApplyDeliversFilesAndRedeploysOnAContentChange(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	const path = "/otel-config.yml"
	without := planFileHeader + applyFileWorker
	withFile := func(content string) string {
		return strings.Replace(without, "    port: 8080", "    port: 8080\n    files: {"+path+": '"+content+"'}", 1)
	}
	previous := ""
	for _, tc := range []struct {
		name string
		file string
		want map[string]string
	}{
		{name: "added", file: withFile("a: 1"), want: map[string]string{path: "a: 1"}},
		{name: "changed", file: withFile("a: 2"), want: map[string]string{path: "a: 2"}},
		{name: "removed", file: without},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.finishRollouts(t)
			applyOK(t, f, tc.file, applyOptions{})
			wantCleanPlan(t, f, tc.file)
			deployments := f.workerDeployments(t)
			if len(deployments) != 1 {
				t.Fatalf("active deployments = %d, want one", len(deployments))
			}
			if deployments[0].ID.String() == previous {
				t.Fatal("the apply started no deployment")
			}
			previous = deployments[0].ID.String()
			got := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Deployment.Files
			if !maps.Equal(got, tc.want) {
				t.Fatalf("placement files = %v, want %v", got, tc.want)
			}
		})
	}
}
