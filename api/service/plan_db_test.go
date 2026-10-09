package service

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const (
	planPartial      = "web"
	planOtherPartial = "backend"
	planNew          = "new"
	planOld          = "old"
	planSvc          = "svc"
	planOwned        = "owned"
	planTheirs       = "theirs"
	testBuildContext = "services/web"
	planRoutedSpec   = `{"routing":{"port":8080,"pathPrefix":"/","idleTimeout":45},` +
		`"regions":{"us-east-1":{"enabled":true,"primary":true,"cpu":"100m",` +
		`"memory":"64Mi","minReplicas":1,"maxReplicas":1}}}`
	planFileHeader = "version: 1\npartial: web\nservices:\n"
	planFileSvc    = `  svc:
    port: 8080
    routing: {}
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	planFileOwned = `  owned:
    port: 8080
    routing: {}
    regions:
      us-east-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	planFileNew = `  new:
    port: 8080
    routing: {}
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	planFileTheirs = `  theirs:
    port: 8080
    routing: {}
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func (f *deployFixture) setSpec(t *testing.T, resourceID uuid.UUID, spec string) {
	t.Helper()
	update := `UPDATE resources SET spec = $2 WHERE id = $1`
	if _, err := f.pool.Exec(context.Background(), update, resourceID, spec); err != nil {
		t.Fatalf("set spec: %v", err)
	}
}

func (f *deployFixture) addResource(t *testing.T, name, partial string) {
	t.Helper()
	insert := `
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version, partial)
VALUES ($1, $2, 'service', '', 'healthy', $3, 1, $4)`
	workspaceID := f.workspaceID(t)
	if _, err := f.pool.Exec(context.Background(), insert, workspaceID, name, planRoutedSpec, partial); err != nil {
		t.Fatalf("add resource %s: %v", name, err)
	}
}

func newPlanServer(f *deployFixture) *PlanServer {
	resolver := &fakeResolver{digest: testDigest}
	return NewPlanServer(f.pool, f.queries, resolver, testRegistryHost, testServiceDefaults())
}

func plan(t *testing.T, f *deployFixture, file string, scopes []genDb.EntityScope) (*planv1.PlanResponse, error) {
	t.Helper()
	server := newPlanServer(f)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&planv1.PlanRequest{File: []byte(file), EnvironmentId: f.envID.String()})
	resp, err := server.Plan(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (f *deployFixture) workspaceReadScopes(t *testing.T) []genDb.EntityScope {
	t.Helper()
	return []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID(t), Scope: genDb.ScopeRead},
	}
}

func seedPlanWorkspace(t *testing.T, f *deployFixture) {
	t.Helper()
	f.setSpec(t, f.resourceID, planRoutedSpec)
	f.addResource(t, planOwned, planPartial)
	f.addResource(t, planOld, planPartial)
	f.addResource(t, planTheirs, planOtherPartial)
	for _, name := range []string{planSvc, planOwned, planOld, planTheirs} {
		f.addSucceededBuild(t, name, testDockerfile, defaultBuildContext)
	}
}

func TestPlanReportsCreateUpdateImportAndDelete(t *testing.T) {
	f := newDeployFixture(t)
	seedPlanWorkspace(t, f)
	file := planFileHeader + planFileNew + planFileSvc + planFileOwned

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetErrors()) != 0 {
		t.Fatalf("errors = %v, want none", resp.GetErrors())
	}
	if resp.GetRevision() != 0 {
		t.Fatalf("revision = %d, want 0", resp.GetRevision())
	}

	kinds := map[string]planv1.PlanOperationKind{}
	for _, op := range resp.GetOperations() {
		kinds[op.GetService()] = op.GetKind()
	}
	want := map[string]planv1.PlanOperationKind{
		planNew:   planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE,
		planOld:   planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE,
		planOwned: planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE,
		planSvc:   planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT,
	}
	if len(kinds) != len(want) {
		t.Fatalf("operations = %v, want %v", resp.GetOperations(), want)
	}
	for service, kind := range want {
		if kinds[service] != kind {
			t.Errorf("%s = %v, want %v", service, kinds[service], kind)
		}
	}

	for _, op := range resp.GetOperations() {
		switch op.GetService() {
		case planNew:
			if !op.GetNeedsDeploy() || op.GetDestructive() {
				t.Errorf("new = %v, want needs_deploy without destructive", op)
			}
		case planOld:
			if !op.GetDestructive() {
				t.Errorf("old = %v, want destructive", op)
			}
		case planOwned:
			changes := op.GetChanges()
			if len(changes) != 1 || changes[0].GetPath() != "regions.us-east-1.cpu" ||
				changes[0].GetBefore() != "100m" || changes[0].GetAfter() != "250m" {
				t.Errorf("owned changes = %v, want only the cpu change", changes)
			}
		case planSvc:
			if len(op.GetChanges()) != 0 {
				t.Errorf("svc changes = %v, want none", op.GetChanges())
			}
		}
	}
}

func (f *deployFixture) addSucceededBuild(t *testing.T, resourceName, dockerfile, buildContext string) {
	t.Helper()
	insert := `
INSERT INTO builds (resource_id, status, source_type, source_key, source_size, dockerfile_path, context,
                    image_repository, image_digest, created_by, finished_at)
SELECT r.id, 'succeeded', 'upload', 'src/' || r.id, 1, $3, $4, 'registry.loco.test/' || r.name, $2, u.id, NOW()
FROM resources r, users u WHERE r.name = $1`
	args := []any{resourceName, testDigest, dockerfile, buildContext}
	if _, err := f.pool.Exec(context.Background(), insert, args...); err != nil {
		t.Fatalf("add build for %s: %v", resourceName, err)
	}
}

func TestPlanReportsChangedBuildInputs(t *testing.T) {
	f := newDeployFixture(t)
	f.setSpec(t, f.resourceID, planRoutedSpec)
	f.addResource(t, planOwned, planPartial)
	f.addSucceededBuild(t, planOwned, "build/Dockerfile", testBuildContext)
	ownedAsLive := strings.Replace(planFileOwned, "250m", "100m", 1)
	file := planFileHeader + ownedAsLive

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 1 {
		t.Fatalf("operations = %v, want one update", resp.GetOperations())
	}
	op := resp.GetOperations()[0]
	if op.GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE || !op.GetNeedsDeploy() {
		t.Fatalf("operation = %v, want an update that needs a deploy", op)
	}
	changes := op.GetChanges()
	if len(changes) != 2 || changes[0].GetPath() != "context" || changes[0].GetBefore() != testBuildContext ||
		changes[1].GetPath() != "dockerfile" || changes[1].GetBefore() != "build/Dockerfile" {
		t.Fatalf("changes = %v, want context and dockerfile", changes)
	}
}

func TestPlanListsNothingForAFileThatMatches(t *testing.T) {
	f := newDeployFixture(t)
	f.setSpec(t, f.resourceID, planRoutedSpec)
	f.addResource(t, planOwned, planPartial)
	f.addSucceededBuild(t, planOwned, testDockerfile, defaultBuildContext)
	ownedAsLive := strings.Replace(planFileOwned, "250m", "100m", 1)
	file := planFileHeader + ownedAsLive

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 0 {
		t.Fatalf("plan = %v, want no operations and no errors", resp)
	}
}

func TestPlanRefusesAnotherPartialsService(t *testing.T) {
	f := newDeployFixture(t)
	seedPlanWorkspace(t, f)
	file := planFileHeader + planFileSvc + planFileOwned + planFileTheirs

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("plan = %v, want one error and no operations", resp)
	}
	planErr := resp.GetErrors()[0]
	ownedByOther := strings.Contains(planErr.GetMessage(), configplan.ErrOwnedByOtherPartial.Error())
	if planErr.GetService() != planTheirs || !ownedByOther {
		t.Fatalf("error = %v, want theirs owned by another partial", planErr)
	}
}

func TestPlanRefusesACustomDomain(t *testing.T) {
	f := newDeployFixture(t)
	file := planFileHeader + strings.Replace(planFileNew, "routing: {}", "domains: [new.acme.dev]", 1)

	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetOperations()) != 0 || len(resp.GetErrors()) != 1 {
		t.Fatalf("plan = %v, want one error and no operations", resp)
	}
	planErr := resp.GetErrors()[0]
	if planErr.GetService() != planNew || planErr.GetPath() != "domains" {
		t.Fatalf("error = %v, want new.acme.dev refused as a custom domain", planErr)
	}
}

func TestPlanNeedsWorkspaceRead(t *testing.T) {
	f := newDeployFixture(t)
	file := planFileHeader + planFileSvc
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeAdmin},
	}
	_, err := plan(t, f, file, scopes)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestPlanRejectsAnInvalidFile(t *testing.T) {
	f := newDeployFixture(t)
	_, err := plan(t, f, "version: 2\n", f.workspaceReadScopes(t))
	wantCode(t, err, connect.CodeInvalidArgument)
}
