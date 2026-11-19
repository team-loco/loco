package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testReservedServiceName = "resource-01234567-89ab-cdef-0123-456789abcdef"

func createNamedResource(t *testing.T, f *deployFixture, name string) (*resourcev1.CreateResourceResponse, error) {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	workspaceID := f.workspaceID(t)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{UserID: f.userID, Scopes: scopes})
	return server.CreateResource(ctx, &resourcev1.CreateResourceRequest{
		WorkspaceId: workspaceID.String(),
		Name:        name,
		Type:        resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec:        testResourceSpec(),
	})
}

func TestCreateResourceRequiresADNSLabelName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	for _, name := range []string{
		"", "QA Echo", "qa echo", "Cart", "🚀rocket", "<script>x</script>", "-bad-", "bad-", "a_b",
		strings.Repeat("a", 64), "localhost", testReservedServiceName,
	} {
		_, err := createNamedResource(t, f, name)
		wantCode(t, err, connect.CodeInvalidArgument)
		if err != nil && !strings.Contains(err.Error(), errInvalidResourceName.Error()) {
			t.Fatalf("%q: error %q", name, err)
		}
	}
	for _, name := range []string{"123cart", "qa-echo", strings.Repeat("a", 63)} {
		created, err := createNamedResource(t, f, name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		resource, err := f.queries.GetResourceByID(t.Context(), uuid.MustParse(created.GetResourceId()))
		if err != nil {
			t.Fatal(err)
		}
		if resource.Name != name || resource.DisplayName != name {
			t.Fatalf("name, display name = %q, %q, want %q", resource.Name, resource.DisplayName, name)
		}
	}
}

func TestCreateResourceRejectsATakenName(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	_, err := createNamedResource(t, f, planSvc)
	wantCode(t, err, connect.CodeAlreadyExists)
	if err != nil && !strings.Contains(err.Error(), ErrResourceNameNotUnique.Error()+": "+planSvc) {
		t.Fatalf("error %q does not name %q", err, planSvc)
	}
}

func TestPlanRejectsNewReservedServiceNames(t *testing.T) {
	t.Parallel()
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
	if response.GetErrors()[0].GetMessage() != errInvalidResourceName.Error() {
		t.Fatalf("error = %v", response.GetErrors()[0])
	}
}
