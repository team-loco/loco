package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testReservedServiceName = "resource-01234567-89ab-cdef-0123-456789abcdef"

func TestCreateResourceRequiresDiscoverableName(t *testing.T) {
	f := newDeployFixture(t)
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	workspaceID := f.workspaceID(t)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	for _, name := range []string{"Invalid_Name", "localhost", testReservedServiceName} {
		req := &resourcev1.CreateResourceRequest{
			WorkspaceId: workspaceID.String(),
			Name:        name,
			Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
			Spec:        testResourceSpec(),
		}
		_, err := server.CreateResource(ctx, req)
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	req := &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        "123cart",
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        testResourceSpec(),
	}
	if _, err := server.CreateResource(ctx, req); err != nil {
		t.Fatalf("numeric DNS label: %v", err)
	}
}

func TestRenameResourceRejectsUnsafeNames(t *testing.T) {
	f := newDeployFixture(t)
	for _, name := range []string{"Invalid_Name", "localhost", testReservedServiceName} {
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
