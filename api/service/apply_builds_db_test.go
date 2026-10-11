package service

import (
	"context"
	"maps"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const applyBuildDockerfile = "build/Dockerfile"

func applyBuilds(t *testing.T, f *deployFixture, file string, builds map[string]string) (*planv1.ApplyResponse, error) {
	t.Helper()
	planned, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("plan before apply: %v", err)
	}
	server := newPlanServer(f)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.adminScopes(t))
	req := connect.NewRequest(&planv1.ApplyRequest{
		File:          []byte(file),
		EnvironmentId: f.envID.String(),
		Revision:      f.revision(t, f.envID),
		Builds:        builds,
		Images:        planned.GetImages(),
	})
	resp, err := server.Apply(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (f *deployFixture) addRunningBuild(t *testing.T, resourceName string) uuid.UUID {
	t.Helper()
	insert := `
INSERT INTO builds (resource_id, status, source_type, source_key, source_size, dockerfile_path, context,
                    image_repository, created_by)
SELECT r.id, 'running', 'upload', 'src/' || gen_random_uuid(), 1, $2, '.', 'registry.loco.test/' || r.name, u.id
FROM resources r, users u WHERE r.name = $1
RETURNING id`
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(), insert, resourceName, applyBuildDockerfile).Scan(&id); err != nil {
		t.Fatalf("add running build for %s: %v", resourceName, err)
	}
	return id
}

func (f *deployFixture) deployedBuild(t *testing.T, started *planv1.StartedDeployment) string {
	t.Helper()
	deploymentID := uuid.MustParse(started.GetDeploymentId())
	deployment, err := f.queries.GetDeploymentByID(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	spec, err := converter.DeserializeDeploymentSpec(deployment.Spec, string(genDb.ResourceTypeService))
	if err != nil {
		t.Fatalf("deployment spec: %v", err)
	}
	build := spec.GetService().GetBuild()
	return build.GetBuildId()
}

func createSourceWorker(t *testing.T, f *deployFixture) string {
	t.Helper()
	f.prepareApply(t)
	file := planFileHeader + applyFileSource
	created := applyOK(t, f, file, applyOptions{})
	if len(created.GetDeployments()) != 0 {
		t.Fatalf("deployments = %v, want none before a build exists", created.GetDeployments())
	}
	return file
}

func TestApplyDeploysASourceServiceFromTheNamedBuild(t *testing.T) {
	f := newDeployFixture(t)
	file := createSourceWorker(t, f)
	buildID := f.addSucceededBuild(t, applyWorker, applyBuildDockerfile, defaultBuildContext)

	resp, err := applyBuilds(t, f, file, map[string]string{applyWorker: buildID.String()})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ops := resp.GetOperations(); len(ops) != 1 || ops[0].GetNeedsDeploy() {
		t.Fatalf("operations = %v, want the update that starts the service", ops)
	}
	if len(resp.GetDeployments()) != 1 {
		t.Fatalf("deployments = %v, want one for %s", resp.GetDeployments(), applyWorker)
	}
	if deployed := f.deployedBuild(t, resp.GetDeployments()[0]); deployed != buildID.String() {
		t.Fatalf("deployment runs build %s, want %s", deployed, buildID)
	}
	wantCleanPlan(t, f, file)

	next := f.addSucceededBuild(t, applyWorker, applyBuildDockerfile, defaultBuildContext)
	again, err := applyBuilds(t, f, file, map[string]string{applyWorker: next.String()})
	if err != nil {
		t.Fatalf("Apply with a new build: %v", err)
	}
	if len(again.GetOperations()) != 0 || len(again.GetDeployments()) != 1 {
		t.Fatalf("response = %v, want no operations and one deployment", again)
	}
	first := uuid.MustParse(resp.GetDeployments()[0].GetDeploymentId())
	if deployed := f.deployedBuild(t, again.GetDeployments()[0]); deployed != next.String() {
		t.Fatalf("deployment runs build %s, want %s", deployed, next)
	}
	if status := f.deploymentStatus(t, first); status != genDb.DeploymentStatusCanceled {
		t.Fatalf("first deployment status = %s, want canceled by the new one", status)
	}
	wantCleanPlan(t, f, file)
}

func TestApplyRefusesBuildsThatCannotDeployTheService(t *testing.T) {
	f := newDeployFixture(t)
	file := createSourceWorker(t, f)
	otherService := f.addSucceededBuild(t, planSvc, testDockerfile, defaultBuildContext)
	running := f.addRunningBuild(t, applyWorker)
	imageFile := planFileHeader + applyFileWorker

	cases := map[string]struct {
		file   string
		builds map[string]string
		want   connect.Code
	}{
		"a service the file lacks": {
			file, map[string]string{planSvc: otherService.String()}, connect.CodeInvalidArgument,
		},
		"another service's build": {
			file, map[string]string{applyWorker: otherService.String()}, connect.CodeNotFound,
		},
		"an unfinished build": {
			file, map[string]string{applyWorker: running.String()}, connect.CodeFailedPrecondition,
		},
		"an image service": {
			imageFile, map[string]string{applyWorker: running.String()}, connect.CodeInvalidArgument,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := applyBuilds(t, f, tc.file, tc.builds)
			wantCode(t, err, tc.want)
		})
	}
	if deployments := f.workerDeployments(t); len(deployments) != 0 {
		t.Fatalf("%d deployments after refused applies, want 0", len(deployments))
	}
}

const (
	provisionImage     = "cache"
	provisionMissing   = "missing"
	provisionFileImage = `  cache:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func applyProvision(
	t *testing.T,
	f *deployFixture,
	file string,
	provision []string,
	builds map[string]string,
) (*planv1.ApplyResponse, error) {
	t.Helper()
	planned, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("plan before apply: %v", err)
	}
	server := newPlanServer(f)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.adminScopes(t))
	req := connect.NewRequest(&planv1.ApplyRequest{
		File:          []byte(file),
		EnvironmentId: f.envID.String(),
		Revision:      f.revision(t, f.envID),
		Images:        planned.GetImages(),
		Provision:     provision,
		Builds:        builds,
	})
	resp, err := server.Apply(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func (f *deployFixture) deploymentCount(t *testing.T, resourceName string) int {
	t.Helper()
	query := `SELECT count(*) FROM deployments d JOIN resources r ON r.id = d.resource_id WHERE r.name = $1`
	var n int
	if err := f.pool.QueryRow(context.Background(), query, resourceName).Scan(&n); err != nil {
		t.Fatalf("count deployments of %s: %v", resourceName, err)
	}
	return n
}

func plannedKinds(t *testing.T, f *deployFixture, file string) map[string]planv1.PlanOperationKind {
	t.Helper()
	resp, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(resp.GetErrors()) > 0 {
		t.Fatalf("plan errors = %v", resp.GetErrors())
	}
	kinds := make(map[string]planv1.PlanOperationKind, len(resp.GetOperations()))
	for _, op := range resp.GetOperations() {
		kinds[op.GetService()] = op.GetKind()
	}
	return kinds
}

func seedProvisionWorkspace(t *testing.T, f *deployFixture) string {
	t.Helper()
	f.prepareApply(t)
	f.addRunningOwned(t)
	f.addResource(t, planOld, planPartial)
	oldBuild := f.addSucceededBuild(t, planOld, testDockerfile, defaultBuildContext)
	f.runBuild(t, planOld, oldBuild)
	return planFileHeader + planFileOwned + applyFileSource + provisionFileImage
}

func TestApplyProvisionCreatesOnlyTheNamedSourceServices(t *testing.T) {
	f := newDeployFixture(t)
	file := seedProvisionWorkspace(t, f)
	before := plannedKinds(t, f, file)
	revision := f.revision(t, f.envID)
	ownedDeployments := f.deploymentCount(t, planOwned)
	oldDeployments := f.deploymentCount(t, planOld)

	resp, err := applyProvision(t, f, file, []string{applyWorker}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ops := resp.GetOperations()
	created := planv1.PlanOperationKind_PLAN_OPERATION_KIND_CREATE
	if len(ops) != 1 || ops[0].GetService() != applyWorker || ops[0].GetKind() != created {
		t.Fatalf("operations = %v, want only the create of %s", ops, applyWorker)
	}
	if len(resp.GetDeployments()) != 0 {
		t.Fatalf("deployments = %v, want none", resp.GetDeployments())
	}
	if resp.GetRevision() <= revision {
		t.Fatalf("revision = %d, want above %d", resp.GetRevision(), revision)
	}
	if !f.hasResource(t, applyWorker) {
		t.Fatalf("%s was not created", applyWorker)
	}
	if f.hasResource(t, provisionImage) {
		t.Fatalf("%s was created, want it left for the final apply", provisionImage)
	}
	if !f.hasResource(t, planOld) {
		t.Fatalf("%s was deleted, want it left for the final apply", planOld)
	}
	if n := f.deploymentCount(t, planOwned); n != ownedDeployments {
		t.Fatalf("%s has %d deployments, want %d", planOwned, n, ownedDeployments)
	}
	if n := f.deploymentCount(t, planOld); n != oldDeployments {
		t.Fatalf("%s has %d deployments, want %d", planOld, n, oldDeployments)
	}

	after := plannedKinds(t, f, file)
	before[applyWorker] = planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE
	if !maps.Equal(after, before) {
		t.Fatalf("plan after provisioning = %v, want %v", after, before)
	}
}

func TestApplyProvisionRefusesServicesThePlanDoesNotCreateFromSource(t *testing.T) {
	f := newDeployFixture(t)
	file := seedProvisionWorkspace(t, f)

	for _, service := range []string{planOwned, planOld, provisionImage, provisionMissing} {
		t.Run(service, func(t *testing.T) {
			_, err := applyProvision(t, f, file, []string{applyWorker, service}, nil)
			refused := wantRefusal(t, err).GetErrors()
			if len(refused) != 1 || refused[0].GetService() != service {
				t.Fatalf("errors = %v, want one for %s", refused, service)
			}
		})
	}
	t.Run("with builds", func(t *testing.T) {
		builds := map[string]string{planOwned: uuid.NewString()}
		_, err := applyProvision(t, f, file, []string{applyWorker}, builds)
		wantCode(t, err, connect.CodeInvalidArgument)
	})
	if f.hasResource(t, applyWorker) {
		t.Fatalf("%s was created by a refused apply", applyWorker)
	}
}
