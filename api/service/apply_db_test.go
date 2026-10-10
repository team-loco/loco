package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const (
	testPlatformDomain = "loco.test"
	applyWorker        = "worker"
	applyWorkerDomain  = "worker.loco.test"
	applyFileWorker    = `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.loco.test]
    env: { LOG_LEVEL: info }
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	applyFileSource = `  worker:
    dockerfile: build/Dockerfile
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

type applyOptions struct {
	revision           int64
	confirmDestructive bool
	confirmImport      bool
	images             map[string]string
	environmentID      uuid.UUID
}

func applyFile(
	t *testing.T,
	f *deployFixture,
	file string,
	opts applyOptions,
	scopes []genDb.EntityScope,
) (*planv1.ApplyResponse, error) {
	t.Helper()
	environmentID := opts.environmentID
	if environmentID == (uuid.UUID{}) {
		environmentID = f.envID
	}
	images := opts.images
	if images == nil {
		if planned, planErr := planIn(t, f, environmentID, file, f.workspaceReadScopes(t)); planErr == nil {
			images = planned.GetImages()
		}
	}
	server := newPlanServer(f)
	ctx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, scopes)
	req := connect.NewRequest(&planv1.ApplyRequest{
		File:               []byte(file),
		EnvironmentId:      environmentID.String(),
		Revision:           opts.revision,
		ConfirmDestructive: opts.confirmDestructive,
		ConfirmImport:      opts.confirmImport,
		Images:             images,
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

func (f *deployFixture) resourceByName(t *testing.T, name string) (genDb.Resource, bool) {
	t.Helper()
	res, err := f.queries.GetResourceByNameAndWorkspace(context.Background(), genDb.GetResourceByNameAndWorkspaceParams{
		WorkspaceID: f.workspaceID(t),
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Resource{}, false
	}
	if err != nil {
		t.Fatalf("get resource %s: %v", name, err)
	}
	return res, true
}

func (f *deployFixture) hasResource(t *testing.T, name string) bool {
	t.Helper()
	_, found := f.resourceByName(t, name)
	return found
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

func TestApplyCreatesAnImageServiceAndDeploysIt(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileWorker

	resp, err := applyFile(t, f, file, applyOptions{}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ops := resp.GetOperations(); len(ops) != 1 || ops[0].GetNeedsDeploy() {
		t.Fatalf("operations = %v, want one create that does not need a deploy", ops)
	}
	if started := resp.GetDeployments(); len(started) != 1 || started[0].GetService() != applyWorker ||
		started[0].GetRegion() != testRegion {
		t.Fatalf("deployments = %v, want one for worker in %s", started, testRegion)
	}
	if resp.GetRevision() == 0 || resp.GetRevision() != f.revision(t, f.envID) {
		t.Fatalf("revision = %d, want the environment's %d", resp.GetRevision(), f.revision(t, f.envID))
	}

	res, found := f.resourceByName(t, applyWorker)
	if !found || derefString(res.Partial) != planPartial {
		t.Fatalf("resource = %+v found %v, want worker owned by %s", res, found, planPartial)
	}
	domains, err := f.queries.ListResourceDomains(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	if len(domains) != 1 || domains[0].Domain != applyWorkerDomain || !domains[0].IsPrimary ||
		domains[0].DomainSource != genDb.DomainSourcePlatformProvided ||
		derefString(domains[0].SubdomainLabel) != applyWorker {
		t.Fatalf("domains = %+v, want the primary platform domain %s", domains, applyWorkerDomain)
	}

	deploymentID := uuid.MustParse(resp.GetDeployments()[0].GetDeploymentId())
	deployment, err := f.queries.GetDeploymentByID(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if !deployment.IsActive || deployment.Replicas != 1 || deployment.EnvironmentID != f.envID {
		t.Fatalf("deployment = %+v, want an active one in the environment with one replica", deployment)
	}
	placementKey := genDb.GetPlacementForResourceClusterParams{ResourceID: res.ID, ClusterID: f.clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(context.Background(), placementKey)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	var payload ApplicationPayload
	if decodeErr := json.Unmarshal(placement.DesiredSpec, &payload); decodeErr != nil {
		t.Fatalf("decode desired spec: %v", decodeErr)
	}
	service := payload.AppSpec.ServiceSpec
	wantImage := "index.docker.io/library/nginx@" + testDigest
	if service.Deployment.Image != wantImage || service.Deployment.Env["LOG_LEVEL"] != "info" {
		t.Fatalf("deployment spec = %+v, want image %s with the file env", service.Deployment, wantImage)
	}
	if service.Routing == nil || service.Routing.HostName != applyWorkerDomain {
		t.Fatalf("routing = %+v, want the primary domain", service.Routing)
	}

	again, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetOperations()) != 0 || len(again.GetErrors()) != 0 {
		t.Fatalf("plan after apply = %v, want no operations", again)
	}
}

func TestApplyCreatesASourceServiceWithoutADeployment(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileSource

	resp, err := applyFile(t, f, file, applyOptions{}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if ops := resp.GetOperations(); len(ops) != 1 || !ops[0].GetNeedsDeploy() {
		t.Fatalf("operations = %v, want one create that needs a deploy", ops)
	}
	if len(resp.GetDeployments()) != 0 {
		t.Fatalf("deployments = %v, want none for a service with no build", resp.GetDeployments())
	}
	res, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker was not created")
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 0 {
		t.Fatalf("%d deployments, want 0", n)
	}
	regions, err := f.queries.ListResourceRegions(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("list regions: %v", err)
	}
	if len(regions) != 1 || regions[0].Region != testRegion || !regions[0].IsPrimary {
		t.Fatalf("regions = %+v, want the primary %s", regions, testRegion)
	}
}

func TestApplyImportsAnUnownedServiceOnConfirmation(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.setRoutedSpec(t)
	f.addSucceededBuild(t, planSvc, testDockerfile, defaultBuildContext)
	file := planFileHeader + planFileSvc

	_, err := applyFile(t, f, file, applyOptions{}, f.applyScopes(t))
	refusal := wantRefusal(t, err)
	if names := unconfirmedServices(refusal); !slices.Equal(names, []string{planSvc}) {
		t.Fatalf("unconfirmed = %v, want [%s]", names, planSvc)
	}
	if res, _ := f.resourceByName(t, planSvc); res.Partial != nil {
		t.Fatalf("partial = %q after a refused apply, want none", *res.Partial)
	}

	resp, err := applyFile(t, f, file, applyOptions{confirmImport: true}, f.applyScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	res, _ := f.resourceByName(t, planSvc)
	if derefString(res.Partial) != planPartial {
		t.Fatalf("partial = %q, want %s", derefString(res.Partial), planPartial)
	}
	if len(resp.GetDeployments()) != 1 {
		t.Fatalf("deployments = %v, want one on the latest build", resp.GetDeployments())
	}
	deploymentID := uuid.MustParse(resp.GetDeployments()[0].GetDeploymentId())
	deployment, err := f.queries.GetDeploymentByID(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	spec, err := converter.DeserializeDeploymentSpec(deployment.Spec, string(res.Type))
	if err != nil {
		t.Fatalf("deployment spec: %v", err)
	}
	build := spec.GetService().GetBuild()
	wantImage := testRegistryHost + "/svc@" + testDigest
	if build.GetType() != buildSourceTypeDockerfile || build.GetImage() != wantImage || build.GetBuildId() == "" {
		t.Fatalf("build = %v, want the latest build pinned as %s", build, wantImage)
	}
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
