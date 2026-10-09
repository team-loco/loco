package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const testPlatformDomain = "loco.test"

type applyOptions struct {
	revision           int64
	confirmDestructive bool
	confirmImport      bool
}

func applyFile(
	t *testing.T,
	f *deployFixture,
	file string,
	opts applyOptions,
	scopes []genDb.EntityScope,
) (*planv1.ApplyResponse, error) {
	t.Helper()
	server := newPlanServer(f)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&planv1.ApplyRequest{
		File:               []byte(file),
		EnvironmentId:      f.envID.String(),
		Revision:           opts.revision,
		ConfirmDestructive: opts.confirmDestructive,
		ConfirmImport:      opts.confirmImport,
	})
	resp, err := server.Apply(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (f *deployFixture) prepareApply(t *testing.T) {
	t.Helper()
	setup := `
WITH c AS (
    UPDATE clusters SET health_status = 'healthy'
)
INSERT INTO platform_domains (domain, is_active) VALUES ($1, true)`
	if _, err := f.pool.Exec(context.Background(), setup, testPlatformDomain); err != nil {
		t.Fatalf("prepare apply: %v", err)
	}
}

func (f *deployFixture) applyScopes(t *testing.T) []genDb.EntityScope {
	t.Helper()
	workspaceID := f.workspaceID(t)
	return []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite},
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead},
	}
}

func (f *deployFixture) adminScopes(t *testing.T) []genDb.EntityScope {
	t.Helper()
	return slices.Concat(f.applyScopes(t), []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeAdmin},
	})
}

func (f *deployFixture) hasResource(t *testing.T, name string) bool {
	t.Helper()
	_, err := f.queries.GetResourceByNameAndWorkspace(context.Background(), genDb.GetResourceByNameAndWorkspaceParams{
		WorkspaceID: f.workspaceID(t),
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("get resource %s: %v", name, err)
	}
	return true
}

func wantRefusal(t *testing.T, err error) *planv1.ApplyRefusal {
	t.Helper()
	wantCode(t, err, connect.CodeFailedPrecondition)
	connectErr, isConnect := errors.AsType[*connect.Error](err)
	if !isConnect {
		t.Fatalf("err = %v, want a connect error", err)
	}
	for _, detail := range connectErr.Details() {
		message, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if refusal, isRefusal := message.(*planv1.ApplyRefusal); isRefusal {
			return refusal
		}
	}
	t.Fatalf("err = %v, want an ApplyRefusal detail", err)
	return nil
}

func unconfirmedServices(refusal *planv1.ApplyRefusal) []string {
	names := make([]string, 0, len(refusal.GetUnconfirmed()))
	for _, op := range refusal.GetUnconfirmed() {
		names = append(names, op.GetService())
	}
	return names
}

func seedOwnedAsLive(t *testing.T, f *deployFixture) string {
	t.Helper()
	f.prepareApply(t)
	f.addResource(t, planOwned, planPartial)
	f.addSucceededBuild(t, planOwned, testDockerfile, defaultBuildContext)
	return planFileHeader + strings.Replace(planFileOwned, "250m", "100m", 1)
}

func TestApplyDeletesAnOwnedServiceOnConfirmation(t *testing.T) {
	f := newDeployFixture(t)
	file := seedOwnedAsLive(t, f)
	f.addResource(t, planOld, planPartial)

	_, err := applyFile(t, f, file, applyOptions{}, f.adminScopes(t))
	refusal := wantRefusal(t, err)
	if names := unconfirmedServices(refusal); !slices.Equal(names, []string{planOld}) {
		t.Fatalf("unconfirmed = %v, want [%s]", names, planOld)
	}

	_, err = applyFile(t, f, file, applyOptions{confirmDestructive: true}, f.applyScopes(t))
	wantCode(t, err, connect.CodePermissionDenied)
	if !f.hasResource(t, planOld) {
		t.Fatal("old was deleted without resource admin")
	}
	if rev := f.revision(t, f.envID); rev != 0 {
		t.Fatalf("revision = %d after refused applies, want 0", rev)
	}

	resp, err := applyFile(t, f, file, applyOptions{confirmDestructive: true}, f.adminScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ops := resp.GetOperations()
	if len(ops) != 1 || ops[0].GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE {
		t.Fatalf("operations = %v, want the delete of old", ops)
	}
	if f.hasResource(t, planOld) {
		t.Fatal("old still exists after the apply")
	}
	if resp.GetRevision() == 0 || resp.GetRevision() != f.revision(t, f.envID) {
		t.Fatalf("revision = %d, want the environment's %d", resp.GetRevision(), f.revision(t, f.envID))
	}

	again, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetOperations()) != 0 || again.GetRevision() != resp.GetRevision() {
		t.Fatalf("plan after apply = %v, want no operations at revision %d", again, resp.GetRevision())
	}
}

func TestApplyRefusesAStaleRevision(t *testing.T) {
	f := newDeployFixture(t)
	file := seedOwnedAsLive(t, f)
	f.addResource(t, planOld, planPartial)

	_, err := applyFile(t, f, file, applyOptions{revision: 1, confirmDestructive: true}, f.adminScopes(t))
	refusal := wantRefusal(t, err)
	if refusal.GetRevision() != 0 || len(refusal.GetUnconfirmed()) != 0 || len(refusal.GetErrors()) != 0 {
		t.Fatalf("refusal = %v, want the current revision 0", refusal)
	}
	if !f.hasResource(t, planOld) {
		t.Fatal("old was deleted by a refused apply")
	}
}

func TestApplyRefusesAPlanWithErrors(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.acme.dev]
    regions:
      mars-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`

	_, err := applyFile(t, f, file, applyOptions{}, f.applyScopes(t))
	refusal := wantRefusal(t, err)
	paths := make([]string, 0, len(refusal.GetErrors()))
	for _, planErr := range refusal.GetErrors() {
		paths = append(paths, planErr.GetPath())
	}
	if !slices.Equal(paths, []string{"regions.mars-1", "domains"}) {
		t.Fatalf("error paths = %v, want the unknown region and the custom domain", paths)
	}
}

func TestApplyNeedsWorkspaceWrite(t *testing.T) {
	f := newDeployFixture(t)
	file := seedOwnedAsLive(t, f)
	_, err := applyFile(t, f, file, applyOptions{}, f.workspaceReadScopes(t))
	wantCode(t, err, connect.CodePermissionDenied)
}
