package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
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
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeRead},
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	req := &environmentv1.GetEnvironmentRequest{EnvironmentId: f.envID.String()}
	resp, err := server.GetEnvironment(ctx, req)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	return resp.GetEnvironment()
}

func TestEnvironmentRevisionFollowsWrites(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	start := getEnvironment(t, f).GetRevision()

	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != start+1 {
		t.Fatalf("revision = %d after a deployment, want %d", rev, start+1)
	}

	f.finishRollouts(t)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != start+2 {
		t.Fatalf("revision = %d after a scale, want %d", rev, start+2)
	}

	client := newDomainClient(t, f)
	if err := client.add(testPrimaryDomain); err != nil {
		t.Fatalf("add domain: %v", err)
	}
	if rev := getEnvironment(t, f).GetRevision(); rev != start+3 {
		t.Fatalf("revision = %d after a domain change, want %d", rev, start+3)
	}

	if rev := getEnvironment(t, f).GetRevision(); rev != start+3 {
		t.Fatalf("revision = %d after reads only, want %d", rev, start+3)
	}
}

func TestRejectedScaleLeavesTheRevisionAlone(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	start := getEnvironment(t, f).GetRevision()
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	f.finishRollouts(t)
	err := scaleResource(t, f, 15)
	wantCode(t, err, connect.CodeInvalidArgument)
	if rev := getEnvironment(t, f).GetRevision(); rev != start+1 {
		t.Fatalf("revision = %d after a rejected scale, want %d", rev, start+1)
	}
}

func updateEnvironment(t *testing.T, f *deployFixture, req *environmentv1.UpdateEnvironmentRequest) {
	t.Helper()
	server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(context.Background(), reqctx.Principal{Scopes: scopes})
	req.EnvironmentId = f.envID.String()
	if _, err := server.UpdateEnvironment(ctx, req); err != nil {
		t.Fatalf("update environment: %v", err)
	}
}

func TestEnvironmentRenameBumpsTheRevision(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	start := getEnvironment(t, f).GetRevision()

	description := "only the description"
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Description: &description})
	if rev := getEnvironment(t, f).GetRevision(); rev != start {
		t.Fatalf("revision = %d after a description change, want %d", rev, start)
	}

	name := "live"
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Name: &name})
	if rev := getEnvironment(t, f).GetRevision(); rev != start+1 {
		t.Fatalf("revision = %d after a rename, want %d", rev, start+1)
	}
}
