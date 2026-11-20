package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	testReservedServiceName = "resource-01234567-89ab-cdef-0123-456789abcdef"
	testNewSlug             = "cart"
	testFreeName            = "QA Echo"
	testRocketName          = "🚀rocket"
	testLocalhost           = "localhost"
)

func createKeyedResource(t *testing.T, f *deployFixture, name string, slug *string) (genDb.Resource, error) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	workspaceID := f.workspaceID(t)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{UserID: f.userID, Scopes: scopes})
	created, err := server.CreateResource(ctx, &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        name,
		Slug:        slug,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        testResourceSpec(),
	})
	if err != nil {
		return genDb.Resource{}, err
	}
	return f.queries.GetResourceByID(t.Context(), uuid.MustParse(created.GetResourceId()))
}

func updateKeys(t *testing.T, f *deployFixture, req *resourcev1.UpdateResourceRequest) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	req.ResourceId = f.resourceID.String()
	_, err := server.UpdateResource(ctx, req)
	return err
}

func wantMessage(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	wantCode(t, err, code)
	if err != nil && !strings.Contains(err.Error(), message) {
		t.Fatalf("error %q, want it to contain %q", err, message)
	}
}

func TestCreateResourceStoresAFreeTextNameAndASlug(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	for _, tc := range []struct {
		name, wantName, wantSlug string
		slug                     *string
	}{
		{name: testFreeName, wantName: testFreeName, wantSlug: "qa-echo"},
		{name: "  Café ☕ Orders  ", wantName: "Café ☕ Orders", wantSlug: "cafe-orders"},
		{name: testRocketName, wantName: testRocketName, wantSlug: "rocket"},
		{name: "Payments API", slug: new("pay"), wantName: "Payments API", wantSlug: "pay"},
		{name: strings.Repeat("🚀", 62) + "x", wantName: strings.Repeat("🚀", 62) + "x", wantSlug: "x"},
	} {
		resource, err := createKeyedResource(t, f, tc.name, tc.slug)
		if err != nil {
			t.Fatalf("%q: %v", tc.name, err)
		}
		if resource.Name != tc.wantName || resource.Slug != tc.wantSlug {
			t.Fatalf(
				"%q: name, slug = %q, %q, want %q, %q", tc.name, resource.Name, resource.Slug, tc.wantName, tc.wantSlug,
			)
		}
	}
}

func TestCreateResourceRejectsAnInvalidName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	for _, name := range []string{"", "   ", strings.Repeat("a", 64), strings.Repeat("🚀", 64)} {
		_, err := createKeyedResource(t, f, name, new("valid"))
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidResourceName.Error())
	}
}

func TestCreateResourceRejectsAnInvalidSlug(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	for _, slug := range []string{
		"", testFreeName, "Cart", testRocketName, "<script>x</script>", "-bad-", "bad-", "a_b",
		strings.Repeat("a", 64), testLocalhost, testReservedServiceName,
	} {
		_, err := createKeyedResource(t, f, "Valid", new(slug))
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidResourceSlug.Error())
	}
	for _, name := range []string{"!!!", "🚀", testLocalhost} {
		_, err := createKeyedResource(t, f, name, nil)
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidResourceSlug.Error())
	}
}

func TestCreateResourceRejectsATakenNameOrSlug(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	_, err := createKeyedResource(t, f, planSvc, new("other"))
	wantMessage(t, err, connect.CodeAlreadyExists, ErrResourceNameNotUnique.Error()+": "+planSvc)
	_, err = createKeyedResource(t, f, strings.ToUpper(planSvc), new("other"))
	wantMessage(t, err, connect.CodeAlreadyExists, ErrResourceNameNotUnique.Error()+": "+strings.ToUpper(planSvc))
	_, err = createKeyedResource(t, f, "Other", new(planSvc))
	wantMessage(t, err, connect.CodeAlreadyExists, ErrResourceSlugNotUnique.Error()+": "+planSvc)
}

func TestUpdateResourceNameKeepsSlugAndPlacements(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Name: new("  Shopping Cart ")}); err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != "Shopping Cart" || resource.Slug != planSvc {
		t.Fatalf("name, slug = %q, %q", resource.Name, resource.Slug)
	}
	after := f.placement(t, f.clusterID)
	if after.DesiredRevision != before.DesiredRevision || !bytes.Equal(after.DesiredSpec, before.DesiredSpec) {
		t.Fatalf("placement changed: revision %d -> %d", before.DesiredRevision, after.DesiredRevision)
	}
}

func TestUpdateResourceRejectsAnInvalidOrTakenName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.addResource(t, testNewSlug, planPartial)
	err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Name: new(testNewSlug)})
	wantMessage(t, err, connect.CodeAlreadyExists, ErrResourceNameNotUnique.Error()+": "+testNewSlug)
	err = updateKeys(t, f, &resourcev1.UpdateResourceRequest{Name: new(" ")})
	wantMessage(t, err, connect.CodeInvalidArgument, errInvalidResourceName.Error())
}

func TestUpdateResourceSlugRewritesLivePlacementsWithoutDeploying(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stagingID := f.deployToProductionAndStaging(t)
	before := []genDb.Placement{f.placementIn(t, f.envID, f.clusterID), f.placementIn(t, stagingID, f.clusterID)}
	count := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`)
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(testNewSlug)}); err != nil {
		t.Fatal(err)
	}
	for _, previous := range before {
		after := f.placementIn(t, previous.EnvironmentID, previous.ClusterID)
		if after.DesiredRevision != previous.DesiredRevision+1 {
			t.Fatalf("revision = %d, want %d", after.DesiredRevision, previous.DesiredRevision+1)
		}
		var original map[string]any
		if err := json.Unmarshal(previous.DesiredSpec, &original); err != nil {
			t.Fatal(err)
		}
		app, ok := original["app_spec"].(map[string]any)
		if !ok {
			t.Fatal("missing application spec")
		}
		app["slug"] = testNewSlug
		want, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(after.DesiredSpec, &fields); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("slug change rewrote more than the slug:\n%s\n%s", got, want)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id=$1`); got != count {
		t.Fatalf("deployments = %d, want %d", got, count)
	}
}

func TestUpdateResourceSlugDoesNotReviveDeletedPlacements(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
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
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(testNewSlug)}); err != nil {
		t.Fatal(err)
	}
	after := f.placement(t, f.clusterID)
	if !after.DesiredDeleted || after.DesiredSpec != nil || after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("deleted placement changed: %+v", after)
	}
}

func TestUpdateResourceSlugRollsBackIfAPlacementCannotBeDecoded(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		context.Background(), `UPDATE placements SET desired_spec='{}' WHERE resource_id=$1`, f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(testNewSlug)}); err == nil {
		t.Fatal("slug change with a malformed placement succeeded")
	}
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Slug != planSvc {
		t.Fatalf("slug = %q, want %s after rollback", resource.Slug, planSvc)
	}
}

func TestUpdateResourceToItsCurrentSlugKeepsPlacementRevision(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(planSvc)}); err != nil {
		t.Fatal(err)
	}
	if after := f.placement(t, f.clusterID); after.DesiredRevision != before.DesiredRevision {
		t.Fatalf("placement revision = %d, want %d", after.DesiredRevision, before.DesiredRevision)
	}
}

func TestUpdateResourceSlugRejectsAnInvalidOrTakenSlug(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.addResource(t, testNewSlug, planPartial)
	err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(testNewSlug)})
	wantMessage(t, err, connect.CodeAlreadyExists, ErrResourceSlugNotUnique.Error()+": "+testNewSlug)
	for _, slug := range []string{testFreeName, testLocalhost, strings.Repeat("a", 64)} {
		err = updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(slug)})
		wantMessage(t, err, connect.CodeInvalidArgument, errInvalidResourceSlug.Error())
	}
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Slug != planSvc {
		t.Fatalf("slug = %q after rejected changes", resource.Slug)
	}
}

func TestUpdateResourceSlugIsBlockedForResourcesLocoYAMLManages(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.pool.Exec(
		context.Background(), `UPDATE resources SET partial = $2 WHERE id = $1`, f.resourceID, planPartial,
	); err != nil {
		t.Fatal(err)
	}
	err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Slug: new(testNewSlug)})
	wantMessage(t, err, connect.CodeFailedPrecondition, errManagedResourceSlug.Error()+": partial "+planPartial)
	if err = updateKeys(t, f, &resourcev1.UpdateResourceRequest{Name: new("Managed name")}); err != nil {
		t.Fatalf("rename a managed resource: %v", err)
	}
}

func TestPlanRejectsNewServicesWithAnInvalidSlugOrATakenName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	if err := updateKeys(t, f, &resourcev1.UpdateResourceRequest{Name: new(planNew)}); err != nil {
		t.Fatal(err)
	}
	for service, message := range map[string]string{
		testReservedServiceName: errInvalidResourceSlug.Error(),
		planNew:                 ErrResourceNameNotUnique.Error() + ": " + planNew,
	} {
		file := planFileHeader + strings.Replace(planFileNew, "  new:", "  "+service+":", 1)
		response, err := plan(t, f, file, f.workspaceReadScopes(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(response.GetErrors()) != 1 || len(response.GetOperations()) != 0 {
			t.Fatalf("%s: plan = %v", service, response)
		}
		if got := response.GetErrors()[0].GetMessage(); got != message {
			t.Fatalf("%s: error = %q, want %q", service, got, message)
		}
	}
}
