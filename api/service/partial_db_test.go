package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const testPartial = "web"

func transferPartial(t *testing.T, f *deployFixture, partial string) error {
	t.Helper()
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeAdmin},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&resourcev1.TransferPartialRequest{
		ResourceId: f.resourceID.String(),
		Partial:    partial,
	})
	_, err := server.TransferPartial(ctx, req)
	return err
}

func (f *deployFixture) addEnvironment(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	insert := `
INSERT INTO environments (workspace_id, name, environment_type, created_by)
SELECT workspace_id, $2, 'dev', (SELECT id FROM users LIMIT 1) FROM resources WHERE id = $1
RETURNING id`
	if err := f.pool.QueryRow(context.Background(), insert, f.resourceID, name).Scan(&id); err != nil {
		t.Fatalf("add environment %s: %v", name, err)
	}
	return id
}

func (f *deployFixture) revision(t *testing.T, environmentID uuid.UUID) int64 {
	t.Helper()
	env, err := f.queries.GetEnvironmentByID(context.Background(), environmentID)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	return env.Revision
}

func TestTransferPartialSetsThePartialAndBumpsEveryEnvironment(t *testing.T) {
	f := newDeployFixture(t)
	devID := f.addEnvironment(t, "dev")
	workspaceID := f.workspaceID(t)
	reader := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}

	before, err := getResourceByName(t, f, reader, workspaceID, "svc")
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if before.GetPartial() != "" {
		t.Fatalf("partial = %q before any transfer, want none", before.GetPartial())
	}

	if transferErr := transferPartial(t, f, testPartial); transferErr != nil {
		t.Fatalf("transfer: %v", transferErr)
	}

	after, err := getResourceByName(t, f, reader, workspaceID, "svc")
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	if after.GetPartial() != testPartial {
		t.Fatalf("partial = %q, want %q", after.GetPartial(), testPartial)
	}
	if rev := f.revision(t, f.envID); rev != 1 {
		t.Fatalf("prod revision = %d, want 1", rev)
	}
	if rev := f.revision(t, devID); rev != 1 {
		t.Fatalf("dev revision = %d, want 1", rev)
	}
}

func TestTransferPartialRejectsTheCurrentPartial(t *testing.T) {
	f := newDeployFixture(t)
	if err := transferPartial(t, f, testPartial); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	err := transferPartial(t, f, testPartial)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if rev := f.revision(t, f.envID); rev != 1 {
		t.Fatalf("revision = %d after a rejected transfer, want 1", rev)
	}

	if err := transferPartial(t, f, "worker"); err != nil {
		t.Fatalf("transfer to another partial: %v", err)
	}
	if rev := f.revision(t, f.envID); rev != 2 {
		t.Fatalf("revision = %d after a second transfer, want 2", rev)
	}
}
