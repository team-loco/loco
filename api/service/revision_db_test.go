package service

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"connectrpc.com/connect/v2"
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
	req := &environmentv1.GetEnvironmentRequest{EnvironmentId: f.envID.String()}
	resp, err := server.GetEnvironment(ctx, req)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	return resp.GetEnvironment()
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
	if _, err := server.UpdateEnvironment(ctx, req); err != nil {
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

func TestEnvironmentDescriptionMaskClearsAndPreservesOtherFields(t *testing.T) {
	f := newDeployFixture(t)
	original := getEnvironment(t, f)
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Description: new("temporary description")})
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"description"}},
		Description: new(""), Name: new("unexpected rename"),
		Type: new(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION),
	})
	got := getEnvironment(t, f)
	if got.GetDescription() != "" || got.GetName() != original.GetName() || got.GetType() != original.GetType() ||
		got.GetRevision() != original.GetRevision() {
		t.Fatalf("description-only update changed the wrong fields: before=%v after=%v", original, got)
	}
}

func TestEnvironmentNameMaskPreservesDescriptionAndType(t *testing.T) {
	f := newDeployFixture(t)
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Description: new("keep this")})
	original := getEnvironment(t, f)
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		Name:       new("new name"), Description: new("ignore this"),
		Type: new(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION),
	})
	got := getEnvironment(t, f)
	if got.GetName() != "new name" || got.GetDescription() != "keep this" || got.GetType() != original.GetType() ||
		got.GetRevision() != original.GetRevision()+1 {
		t.Fatalf("name-only update changed the wrong fields: %v", got)
	}
}

func TestEnvironmentRejectsInvalidMaskedUpdates(t *testing.T) {
	f := newDeployFixture(t)
	server := NewEnvironmentServer(f.pool, f.queries, nil)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	for _, path := range []string{"environment_id", "missing", "name", "type"} {
		_, err := server.UpdateEnvironment(ctx, &environmentv1.UpdateEnvironmentRequest{
			EnvironmentId: f.envID.String(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
		})
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	if revision := getEnvironment(t, f).GetRevision(); revision != 0 {
		t.Fatalf("invalid mask changed revision to %d", revision)
	}
}

func TestEnvironmentWildcardMaskClearsDescription(t *testing.T) {
	f := newDeployFixture(t)
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{Description: new("clear this")})
	updateEnvironment(t, f, &environmentv1.UpdateEnvironmentRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"*"}},
		Name:       new("all fields"), Type: new(environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION),
	})
	got := getEnvironment(t, f)
	if got.GetName() != "all fields" || got.GetDescription() != "" ||
		got.GetType() != environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION ||
		got.GetRevision() != 1 {
		t.Fatalf("wildcard update = %v", got)
	}
}
