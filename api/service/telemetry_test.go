package service

import (
	"strings"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
)

func TestApplyInjectOtelPersistsAndDisables(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, inject := range []bool{true, false, true} {
		file := base
		if !inject {
			file = strings.Replace(base, "    port: 8080", "    port: 8080\n    observability: {injectOtel: false}", 1)
		}
		f.finishRollouts(t)
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 {
			t.Fatalf("active deployments=%d", len(deployments))
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		settings := stored.GetService().GetObservability()
		if settings.InjectOtel == nil || settings.GetInjectOtel() != inject {
			t.Fatalf("snapshot=%v want injectOtel=%v", settings, inject)
		}
		if got := f.workerPayload(
			t,
			f.clusterID,
		).AppSpec.ServiceSpec.Deployment.Observability.InjectOtel; got != inject {
			t.Fatalf("pod injection=%v want=%v", got, inject)
		}
	}
}
