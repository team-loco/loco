package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	testReservedServiceName = "resource-01234567-89ab-cdef-0123-456789abcdef"
	testDisplayName         = "Shopping Cart"
	testDisplaySlug         = "shopping-cart"
)

func createNamedResource(t *testing.T, f *deployFixture, name string) (*resourcev1.CreateResourceResponse, error) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	workspaceID := f.workspaceID(t)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	return server.CreateResource(ctx, &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        testResourceSpec(),
	})
}

func TestCreateResourceRequiresAHostnameSlug(t *testing.T) {
	f := newDeployFixture(t)
	for _, name := range []string{"", "!!!", "localhost", "Localhost!", testReservedServiceName} {
		_, err := createNamedResource(t, f, name)
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	if _, err := createNamedResource(t, f, "123cart"); err != nil {
		t.Fatalf("numeric DNS label: %v", err)
	}
}

func TestCreateResourceStoresTheNameSlug(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createNamedResource(t, f, testDisplayName)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(context.Background(), uuid.MustParse(created.GetResourceId()))
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != testDisplayName || resource.Slug != testDisplaySlug {
		t.Fatalf("name, slug = %q, %q", resource.Name, resource.Slug)
	}
}

func TestCreateResourceRejectsATakenSlug(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createNamedResource(t, f, testDisplayName); err != nil {
		t.Fatal(err)
	}
	_, err := createNamedResource(t, f, "shopping cart!")
	wantCode(t, err, connect.CodeAlreadyExists)
	if !strings.Contains(err.Error(), testDisplayName) {
		t.Fatalf("error %q does not name %q", err, testDisplayName)
	}
}

func TestRenameResourceRejectsUnsafeNames(t *testing.T) {
	f := newDeployFixture(t)
	for _, name := range []string{"!!!", "localhost", testReservedServiceName} {
		err := renameResource(t, f, name)
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != planSvc {
		t.Fatalf("name = %q after rejected renames", resource.Name)
	}
}

func TestRenameResourceMovesTheSlug(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if err := renameResource(t, f, testDisplayName); err != nil {
		t.Fatal(err)
	}
	resource, err := f.queries.GetResourceByID(context.Background(), f.resourceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Name != testDisplayName || resource.Slug != testDisplaySlug {
		t.Fatalf("name, slug = %q, %q", resource.Name, resource.Slug)
	}
	var payload ApplicationPayload
	if err := json.Unmarshal(f.placement(t, f.clusterID).DesiredSpec, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ResourceName != testDisplayName || payload.AppSpec.Slug != testDisplaySlug {
		t.Fatalf("placement name, slug = %q, %q", payload.ResourceName, payload.AppSpec.Slug)
	}
	if _, err := createNamedResource(t, f, planSvc); err != nil {
		t.Fatalf("old slug still held after rename: %v", err)
	}
}

func TestRenameResourceRejectsATakenSlug(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createNamedResource(t, f, testDisplayName); err != nil {
		t.Fatal(err)
	}
	err := renameResource(t, f, "SHOPPING-CART")
	wantCode(t, err, connect.CodeAlreadyExists)
	if !strings.Contains(err.Error(), testDisplayName) {
		t.Fatalf("error %q does not name %q", err, testDisplayName)
	}
}

func TestPlanRejectsNewReservedServiceNames(t *testing.T) {
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	file := planFileHeader + strings.Replace(planFileNew, "  new:", "  "+testReservedServiceName+":", 1)
	response, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetErrors()) != 1 || len(response.GetOperations()) != 0 {
		t.Fatalf("reserved create plan = %v", response)
	}
	if response.GetErrors()[0].GetMessage() != errPrivateServiceName.Error() {
		t.Fatalf("error = %v", response.GetErrors()[0])
	}
}

func TestPlanRejectsCreatesWithATakenSlug(t *testing.T) {
	f := newDeployFixture(t)
	f.markClustersHealthy(t)
	file := planFileHeader + strings.Replace(planFileNew, "  new:", "  SVC:", 1) +
		planFileNew + strings.Replace(planFileNew, "  new:", "  New!:", 1)
	response, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetErrors()) != 2 || len(response.GetOperations()) != 0 {
		t.Fatalf("taken slug plan = %v", response)
	}
	for _, planErr := range response.GetErrors() {
		if !strings.Contains(planErr.GetMessage(), errSlugInUse.Error()) {
			t.Fatalf("error = %v", planErr)
		}
	}
}
