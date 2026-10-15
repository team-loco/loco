package service

import (
	"context"
	"encoding/json"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testRenamedResource = "cart"

func renameResource(t *testing.T, f *deployFixture, name string) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	_, err := server.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), Name: new(name)},
	)
	return err
}

func TestRenameResourceUpdatesAllLivePlacementsWithoutDeploying(t *testing.T) {
	f := newDeployFixture(t)
	f.deployToProductionAndStaging(t)
	before := []genDb.Placement{f.placement(t, f.clusterID), f.placement(t, f.otherCluster)}
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	if err := renameResource(t, f, testRenamedResource); err != nil {
		t.Fatal(err)
	}
	for _, previous := range before {
		after := f.placement(t, previous.ClusterID)
		desired, revision := after.DesiredSpec, after.DesiredRevision
		var fields map[string]any
		if err := json.Unmarshal(desired, &fields); err != nil {
			t.Fatal(err)
		}
		app, ok := fields["app_spec"].(map[string]any)
		if !ok {
			t.Fatal("missing application spec")
		}
		if fields["resource_name"] != testRenamedResource || app["resourceName"] != testRenamedResource {
			t.Fatalf(
				"cluster %s desired name = %v/%v, want cart/cart",
				previous.ClusterID,
				fields["resource_name"],
				app["resourceName"],
			)
		}
		if revision != previous.DesiredRevision+1 {
			t.Fatalf("revision = %d, want %d", revision, previous.DesiredRevision+1)
		}
		var original map[string]any
		if err := json.Unmarshal(previous.DesiredSpec, &original); err != nil {
			t.Fatal(err)
		}
		originalApp, ok := original["app_spec"].(map[string]any)
		if !ok {
			t.Fatal("missing original application spec")
		}
		original["resource_name"] = testRenamedResource
		originalApp["resourceName"] = testRenamedResource
		want, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("rename changed deployment fields:\n%s\n%s", got, want)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != count {
		t.Fatalf("deployments = %d, want %d", got, count)
	}
}

func TestRenameResourceDoesNotReviveDeletedPlacements(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		context.Background(),
		`UPDATE placements SET desired_deleted=true,desired_spec=NULL WHERE resource_id=$1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := renameResource(t, f, testRenamedResource); err != nil {
		t.Fatal(err)
	}
	after := f.placement(t, f.clusterID)
	if !after.DesiredDeleted || after.DesiredSpec != nil || after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("deleted placement changed: %+v", after)
	}
}

func TestRenameResourceRollsBackIfAPlacementCannotBeDecoded(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		context.Background(),
		`UPDATE placements SET desired_spec='{}' WHERE resource_id=$1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	if err := renameResource(t, f, testRenamedResource); err == nil {
		t.Fatal("rename with a malformed placement succeeded")
	}
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != planSvc {
		t.Fatalf("resource name = %q, want svc after rollback", resource.Name)
	}
}

func TestRenameResourceToItsCurrentNameKeepsPlacementRevision(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := renameResource(t, f, planSvc); err != nil {
		t.Fatal(err)
	}
	after := f.placement(t, f.clusterID)
	if after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("placement revision = %d, want %d", after.DesiredRevision, before.DesiredRevision)
	}
}

func TestRenameResourceCannotTakeAnotherResourcesName(t *testing.T) {
	f := newDeployFixture(t)
	f.addResource(t, testRenamedResource, planPartial)
	err := renameResource(t, f, testRenamedResource)
	wantCode(t, err, connect.CodeAlreadyExists)
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != planSvc {
		t.Fatalf("resource name = %q after conflict", resource.Name)
	}
}
