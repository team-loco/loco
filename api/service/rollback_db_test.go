package service

import (
	"strings"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCreateDeploymentStoresResolvedRollbackOnFailure(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		f := newDeployFixture(t)
		spec := sameRegionSpec
		if enabled {
			spec = strings.Replace(spec, "{", `{"rollbackOnFailure":true,`, 1)
		}
		if _, err := createDeployment(t, f, spec); err != nil {
			t.Fatal(err)
		}
		var stored []byte
		query := `SELECT spec FROM deployments WHERE resource_id = $1`
		if err := f.pool.QueryRow(t.Context(), query, f.resourceID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		got := &deploymentv1.ServiceDeploymentSpec{}
		if err := protojson.Unmarshal(stored, got); err != nil {
			t.Fatal(err)
		}
		if got.RollbackOnFailure == nil || got.GetRollbackOnFailure() != enabled {
			t.Fatalf("snapshot=%v, want rollback=%v", got, enabled)
		}
	}
}

func TestApplyRollbackOnFailurePersistsAndClears(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	for _, enabled := range []bool{false, true, false} {
		file := base
		if enabled {
			file = strings.Replace(base, "    port: 8080", "    port: 8080\n    rollbackOnFailure: true", 1)
		}
		f.finishRollouts(t)
		applyOK(t, f, file, applyOptions{})
		wantCleanPlan(t, f, file)
		resource := f.workerResource(t)
		resourceSpec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
		if err != nil {
			t.Fatal(err)
		}
		if got := resourceSpec.GetService(); got.RollbackOnFailure == nil || got.GetRollbackOnFailure() != enabled {
			t.Fatalf("resource=%v, want rollback=%v", got, enabled)
		}
		deployments := f.workerDeployments(t)
		if len(deployments) != 1 {
			t.Fatalf("active deployments=%d", len(deployments))
		}
		stored, err := converter.DeserializeDeploymentSpec(deployments[0].Spec, genDb.ResourceTypeService)
		if err != nil {
			t.Fatal(err)
		}
		got := stored.GetService()
		if got.RollbackOnFailure == nil || got.GetRollbackOnFailure() != enabled {
			t.Fatalf("snapshot=%v, want rollback=%v", got, enabled)
		}
	}
}
