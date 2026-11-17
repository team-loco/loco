package service

import (
	"bytes"
	"strings"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/pkg/converter"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCreateDeploymentPreservesRollbackOverride(t *testing.T) {
	t.Parallel()
	for _, override := range []*bool{nil, new(false), new(true)} {
		f := newDeployFixture(t)
		spec := strings.Replace(sameRegionSpec, "{", `{"rollbackOnFailure":true,`, 1)
		request := imageService()
		request.RollbackOnFailure = override
		if _, err := createDeploymentWith(t, f, spec, request); err != nil {
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
		if (got.RollbackOnFailure == nil) != (override == nil) ||
			(override != nil && got.GetRollbackOnFailure() != *override) {
			t.Fatalf("snapshot=%v, want override=%v", got, override)
		}
	}
}

func TestUpdateRollbackOnFailureDoesNotDeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.deployToProductionAndStaging(t)
	before := []genDb.Placement{f.placement(t, f.clusterID), f.placement(t, f.otherCluster)}
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		UserID: f.userID, ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb,
		Scopes: []genDb.EntityScope{
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
		},
	})
	for _, enabled := range []bool{true, false} {
		_, err := server.UpdateResource(
			ctx,
			&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), RollbackOnFailure: new(enabled)},
		)
		if err != nil {
			t.Fatal(err)
		}
		res, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
		if err != nil {
			t.Fatal(err)
		}
		if spec.GetService().GetRollbackOnFailure() != enabled {
			t.Fatalf("resource=%v", spec)
		}
		if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != count {
			t.Fatalf("deployments=%d, want %d", got, count)
		}
		for _, previous := range before {
			after := f.placement(t, previous.ClusterID)
			if after.DesiredRevision != previous.DesiredRevision ||
				!bytes.Equal(after.DesiredSpec, previous.DesiredSpec) {
				t.Fatal("setting change modified placement")
			}
		}
	}
}

func TestApplyRollbackOnFailureDoesNotDeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	base := planFileHeader + applyFileWorker
	applyOK(t, f, base, applyOptions{})
	before := f.workerPlacement(t)
	resource := f.workerResource(t)
	overrideQuery := `UPDATE deployments SET spec=jsonb_set(spec, '{rollbackOnFailure}', 'true') WHERE resource_id=$1`
	if _, err := f.pool.Exec(t.Context(), overrideQuery, resource.ID); err != nil {
		t.Fatal(err)
	}
	wantCleanPlan(t, f, base)
	var count int
	query := `SELECT count(*) FROM deployments WHERE resource_id=$1`
	if err := f.pool.QueryRow(t.Context(), query, resource.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		file := base
		if enabled {
			file = strings.Replace(base, "    port: 8080", "    port: 8080\n    rollbackOnFailure: true", 1)
		}
		response := applyOK(t, f, file, applyOptions{})
		if len(response.GetDeployments()) != 0 {
			t.Fatalf("started deployments=%v", response.GetDeployments())
		}
		wantCleanPlan(t, f, file)
		resource = f.workerResource(t)
		spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
		if err != nil {
			t.Fatal(err)
		}
		if spec.GetService().GetRollbackOnFailure() != enabled {
			t.Fatalf("resource=%v", spec)
		}
		var got int
		if err := f.pool.QueryRow(t.Context(), query, resource.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != count {
			t.Fatalf("deployments=%d, want %d", got, count)
		}
		after := f.workerPlacement(t)
		if after.DesiredRevision != before.DesiredRevision || !bytes.Equal(after.DesiredSpec, before.DesiredSpec) {
			t.Fatal("setting change modified placement")
		}
	}
}

func TestUpdateRollbackWithRegionsPreservesBothSettings(t *testing.T) {
	t.Parallel()
	f := prepareRegionUpdate(t)
	targets, revision := regionTargets(t, f)
	targets[testRegion].Cpu = biggerCPU
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		UserID: f.userID, ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb,
		Scopes: []genDb.EntityScope{
			{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
		},
	})
	_, err := server.UpdateResource(ctx, &resourcev1.UpdateResourceRequest{
		ResourceId: f.resourceID.String(), Regions: &resourcev1.RegionTargets{Targets: targets},
		Revision: revision, RollbackOnFailure: new(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := converter.DeserializeResourceSpec(resource.Spec, resource.Type)
	if err != nil {
		t.Fatal(err)
	}
	if service := spec.GetService(); !service.GetRollbackOnFailure() ||
		service.GetRegions()[testRegion].GetCpu() != biggerCPU {
		t.Fatalf("combined update lost a setting: %v", spec)
	}
}
