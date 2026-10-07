package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func getResourceByName(
	t *testing.T,
	f *deployFixture,
	scopes []genDb.EntityScope,
	workspaceID uuid.UUID,
	name string,
) (*resourcev1.Resource, error) {
	t.Helper()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewResourceServer(f.pool, f.queries, machine, testServiceDefaults())
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	nameKey := &resourcev1.GetResourceNameKey{WorkspaceId: workspaceID.String(), Name: name}
	req := connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{NameKey: nameKey},
	})
	resp, err := server.GetResource(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetResource(), nil
}

func TestGetResourceByName(t *testing.T) {
	f := newDeployFixture(t)
	var workspaceID uuid.UUID
	if err := f.pool.QueryRow(context.Background(), `SELECT workspace_id FROM resources WHERE id = $1`, f.resourceID).
		Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	reader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
	outsider := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: uuid.New(), Scope: genDb.ScopeRead},
	}

	res, err := getResourceByName(t, f, reader, workspaceID, "svc")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if res.GetId() != f.resourceID.String() {
		t.Fatalf("resource = %s, want %s", res.GetId(), f.resourceID)
	}

	_, err = getResourceByName(t, f, reader, workspaceID, "missing")
	wantCode(t, err, connect.CodeNotFound)

	_, err = getResourceByName(t, f, outsider, workspaceID, "missing")
	wantCode(t, err, connect.CodePermissionDenied)

	_, err = getResourceByName(t, f, outsider, workspaceID, "svc")
	wantCode(t, err, connect.CodePermissionDenied)
}
