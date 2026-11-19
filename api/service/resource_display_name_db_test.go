package service

import (
	"bytes"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testDisplayName = "Shopping Cart"

func setDisplayName(t *testing.T, f *deployFixture, displayName string) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
	_, err := server.UpdateResource(
		ctx,
		&resourcev1.UpdateResourceRequest{ResourceId: f.resourceID.String(), DisplayName: new(displayName)},
	)
	return err
}

func TestUpdateResourceDisplayNameKeepsNameAndPlacements(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := setDisplayName(t, f, testDisplayName); err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.DisplayName != testDisplayName || resource.Name != planSvc {
		t.Fatalf("display name, name = %q, %q", resource.DisplayName, resource.Name)
	}
	after := f.placement(t, f.clusterID)
	if after.DesiredRevision != before.DesiredRevision || !bytes.Equal(after.DesiredSpec, before.DesiredSpec) {
		t.Fatalf("placement changed: revision %d -> %d", before.DesiredRevision, after.DesiredRevision)
	}
}

func TestResourcesMayShareADisplayName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.addResource(t, "cart", planPartial)
	other, err := f.queries.GetResourceByNameAndWorkspace(
		t.Context(),
		genDb.GetResourceByNameAndWorkspaceParams{WorkspaceID: f.workspaceID(t), Name: "cart"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := setDisplayName(t, f, other.DisplayName); err != nil {
		t.Fatal(err)
	}
}
