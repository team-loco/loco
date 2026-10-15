package service

import (
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestApplyRuntimeUserPersistsAndClears(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, id := range []int64{1654, 82, 0} {
		file := base
		if id == 1654 {
			file = strings.Replace(
				base,
				"    port: 8080",
				"    port: 8080\n    runAsUser: 1654\n    runAsGroup: 1654",
				1,
			)
		}
		if id == 82 {
			file = strings.Replace(base, "    port: 8080", "    port: 8080\n    runAsUser: 82\n    runAsGroup: 82", 1)
		}
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 {
			t.Fatalf("active deployments = %d", len(deployments))
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, "service")
		if err != nil {
			t.Fatal(err)
		}
		if stored.GetService().GetRunAsUser() != id || stored.GetService().GetRunAsGroup() != id {
			t.Fatalf("snapshot = %v", stored)
		}
		deployment := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Deployment
		if id == 0 {
			if deployment.RunAsUser != nil || deployment.RunAsGroup != nil {
				t.Fatalf("runtime overrides not cleared: %+v", deployment)
			}
		} else if deployment.RunAsUser == nil || *deployment.RunAsUser != id ||
			deployment.RunAsGroup == nil || *deployment.RunAsGroup != id {
			t.Fatalf("placement = %+v", deployment)
		}
	}
}

func TestRuntimeUserWireValidation(t *testing.T) {
	for _, id := range []int64{-1, 0, 82, 1654} {
		service := &deploymentv1.ServiceDeploymentSpec{
			Build:      &deploymentv1.BuildSource{Type: "image"},
			Port:       8080,
			RunAsUser:  &id,
			RunAsGroup: &id,
		}
		if err := protovalidate.Validate(service); (err != nil) != (id <= 0) {
			t.Fatalf("ID %d validation: %v", id, err)
		}
		encoded, err := protojson.Marshal(service)
		if err != nil {
			t.Fatal(err)
		}
		var decoded deploymentv1.ServiceDeploymentSpec
		if err := protojson.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.GetRunAsUser() != id || decoded.GetRunAsGroup() != id {
			t.Fatalf("wire round trip = %v", &decoded)
		}
	}
}
