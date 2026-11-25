package service

import (
	"fmt"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestApplyRuntimeUserPersistsAndClears(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, id := range []*int64{new(int64(1654)), new(int64(0)), nil} {
		file := base
		if id != nil {
			file = strings.Replace(
				base, "    port: 8080",
				fmt.Sprintf("    port: 8080\n    runAsUser: %d\n    runAsGroup: %d", *id, *id), 1,
			)
		}
		f.finishRollouts(t)
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 {
			t.Fatalf("active deployments = %d", len(deployments))
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		want := &deploymentv1.ServiceDeploymentSpec{RunAsUser: id, RunAsGroup: id}
		if stored.GetService().GetRunAsUser() != want.GetRunAsUser() ||
			stored.GetService().GetRunAsGroup() != want.GetRunAsGroup() {
			t.Fatalf("snapshot = %v", stored)
		}
		deployment := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Deployment
		for _, got := range []*int64{deployment.RunAsUser, deployment.RunAsGroup} {
			if id == nil {
				if got != nil {
					t.Fatalf("runtime override not cleared: %d", *got)
				}
			} else if got == nil || *got != *id {
				t.Fatalf("runtime ID = %v, want %d", got, *id)
			}
		}
	}
}

func TestRuntimeUserWireValidation(t *testing.T) {
	t.Parallel()
	for _, id := range []int64{-1, 0, 82, 1654, 65535, 65536} {
		service := &deploymentv1.ServiceDeploymentSpec{
			Build:      &deploymentv1.BuildSource{Type: "image"},
			Port:       new(int32(8080)),
			RunAsUser:  &id,
			RunAsGroup: &id,
		}
		if err := protovalidate.Validate(service); (err != nil) != (id < 0 || id > 65535) {
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
