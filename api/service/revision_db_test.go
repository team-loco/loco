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
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func (f *deployFixture) workspaceID(t *testing.T) uuid.UUID {
	t.Helper()
	var workspaceID uuid.UUID
	query := `SELECT workspace_id FROM resources WHERE id = $1`
	if err := f.pool.QueryRow(context.Background(), query, f.resourceID).Scan(&workspaceID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	return workspaceID
}

func getEnvironment(t *testing.T, f *deployFixture) *environmentv1.Environment {
	t.Helper()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewEnvironmentServer(f.pool, f.queries, machine)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeRead},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&environmentv1.GetEnvironmentRequest{EnvironmentId: f.envID.String()})
	resp, err := server.GetEnvironment(ctx, req)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	return resp.Msg.GetEnvironment()
}

func TestEnvironmentRevisionFollowsWrites(t *testing.T) {
	f := newDeployFixture(t)
	if rev := getEnvironment(t, f).GetRevision(); rev != 0 {
		t.Fatalf("revision = %d on a new environment, want 0", rev)
	}

	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != 1 {
		t.Fatalf("revision = %d after a deployment, want 1", rev)
	}

	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != 2 {
		t.Fatalf("revision = %d after a scale, want 2", rev)
	}

	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatalf("add domain: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != 3 {
		t.Fatalf("revision = %d after a domain change, want 3", rev)
	}

	if rev := getEnvironment(t, f).GetRevision(); rev != 3 {
		t.Fatalf("revision = %d after reads only, want 3", rev)
	}
}

func TestRejectedScaleLeavesTheRevisionAlone(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	err := scaleResource(t, f, 15)
	wantCode(t, err, connect.CodeInvalidArgument)
	if rev := getEnvironment(t, f).GetRevision(); rev != 1 {
		t.Fatalf("revision = %d after a rejected scale, want 1", rev)
	}
}

func updateEnvironment(t *testing.T, f *deployFixture, req *environmentv1.UpdateEnvironmentRequest) {
	t.Helper()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{LastUsedUpdateInterval: time.Minute})
	t.Cleanup(machine.Close)
	server := NewEnvironmentServer(f.pool, f.queries, machine)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req.EnvironmentId = f.envID.String()
	if _, err := server.UpdateEnvironment(ctx, connect.NewRequest(req)); err != nil {
		t.Fatalf("update environment: %v", err)
	}
}

func TestEnvironmentRenameAndRetypeBumpTheRevision(t *testing.T) {
	f := newDeployFixture(t)

	description := "only the description"
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Description: &description})
	if rev := getEnvironment(t, f).GetRevision(); rev != 0 {
		t.Fatalf("revision = %d after a description change, want 0", rev)
	}

	name := "production"
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Name: &name})
	if rev := getEnvironment(t, f).GetRevision(); rev != 1 {
		t.Fatalf("revision = %d after a rename, want 1", rev)
	}

	staging := environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Type: &staging})
	if rev := getEnvironment(t, f).GetRevision(); rev != 2 {
		t.Fatalf("revision = %d after a type change, want 2", rev)
	}
}
