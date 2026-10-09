package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/converter"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const (
	otherRegion          = "eu-west-1"
	biggerCPU            = "250m"
	applyAPIDomain       = "api.loco.test"
	applyFileWorkerDebug = `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.loco.test]
    env: { LOG_LEVEL: debug }
    regions:
      us-east-1: { cpu: 250m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	applyFileTwoRegions = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
      eu-west-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	applyFileOtherRegion = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      eu-west-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func applyOK(t *testing.T, f *deployFixture, file string, opts applyOptions) *planv1.ApplyResponse {
	t.Helper()
	resp, err := applyFile(t, f, file, applyOptions{
		revision:           f.revision(t, f.envID),
		confirmDestructive: opts.confirmDestructive,
		confirmImport:      opts.confirmImport,
	}, f.adminScopes(t))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return resp
}

func wantCleanPlan(t *testing.T, f *deployFixture, file string) {
	t.Helper()
	again, err := plan(t, f, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetOperations()) != 0 || len(again.GetErrors()) != 0 {
		t.Fatalf("plan after apply = %v, want no operations", again)
	}
}

func (f *deployFixture) workerPayload(t *testing.T, clusterID uuid.UUID) ApplicationPayload {
	t.Helper()
	res, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker does not exist")
	}
	key := genDb.GetPlacementForResourceClusterParams{ResourceID: res.ID, ClusterID: clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(context.Background(), key)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	var payload ApplicationPayload
	if decodeErr := json.Unmarshal(placement.DesiredSpec, &payload); decodeErr != nil {
		t.Fatalf("decode desired spec: %v", decodeErr)
	}
	return payload
}

func (f *deployFixture) workerDeployments(t *testing.T) []genDb.Deployment {
	t.Helper()
	res, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker does not exist")
	}
	deployments, err := f.queries.ListActiveDeploymentsForResource(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("list active deployments: %v", err)
	}
	return deployments
}

func TestApplyUpdateRollsADeploymentForRuntimeChanges(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	first := applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	firstDeployment := uuid.MustParse(first.GetDeployments()[0].GetDeploymentId())

	resp := applyOK(t, f, planFileHeader+applyFileWorkerDebug, applyOptions{})
	ops := resp.GetOperations()
	if len(ops) != 1 || ops[0].GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE ||
		len(ops[0].GetChanges()) != 2 {
		t.Fatalf("operations = %v, want one update with the env and cpu changes", ops)
	}
	if len(resp.GetDeployments()) != 1 || resp.GetDeployments()[0].GetDeploymentId() == firstDeployment.String() {
		t.Fatalf("deployments = %v, want one new deployment", resp.GetDeployments())
	}
	if status := f.deploymentStatus(t, firstDeployment); status != genDb.DeploymentStatusCanceled {
		t.Fatalf("first deployment status = %s, want canceled by the new one", status)
	}
	service := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec
	if service.Deployment.Env["LOG_LEVEL"] != "debug" || service.Resources.CPU != biggerCPU {
		t.Fatalf("desired spec = %+v, want LOG_LEVEL debug and cpu %s", service, biggerCPU)
	}
	res, _ := f.resourceByName(t, applyWorker)
	spec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		t.Fatalf("resource spec: %v", err)
	}
	if cpu := spec.GetService().GetRegions()[testRegion].GetCpu(); cpu != biggerCPU {
		t.Fatalf("resource spec cpu = %s, want %s", cpu, biggerCPU)
	}
	wantCleanPlan(t, f, planFileHeader+applyFileWorkerDebug)
}

func TestApplyUpdateReplacesDomainsAndReroutes(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	twoDomains := strings.Replace(applyFileWorker, "[worker.loco.test]", "[api.loco.test, worker.loco.test]", 1)

	resp := applyOK(t, f, planFileHeader+twoDomains, applyOptions{})
	if len(resp.GetDeployments()) != 1 {
		t.Fatalf("deployments = %v, want one for the new route", resp.GetDeployments())
	}
	res, _ := f.resourceByName(t, applyWorker)
	domains, err := f.queries.ListResourceDomains(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	if len(domains) != 2 || domains[0].Domain != applyAPIDomain || !domains[0].IsPrimary || domains[1].IsPrimary {
		t.Fatalf("domains = %+v, want api primary and worker secondary", domains)
	}
	if routing := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec.Routing; routing.HostName != applyAPIDomain {
		t.Fatalf("routed hostname = %s, want %s", routing.HostName, applyAPIDomain)
	}
	wantCleanPlan(t, f, planFileHeader+twoDomains)

	onlyAPI := strings.Replace(applyFileWorker, "[worker.loco.test]", "[api.loco.test]", 1)
	applyOK(t, f, planFileHeader+onlyAPI, applyOptions{})
	domains, err = f.queries.ListResourceDomains(context.Background(), res.ID)
	if err != nil {
		t.Fatalf("list domains: %v", err)
	}
	if len(domains) != 1 || domains[0].Domain != applyAPIDomain || !domains[0].IsPrimary {
		t.Fatalf("domains = %+v, want only api as primary", domains)
	}
	wantCleanPlan(t, f, planFileHeader+onlyAPI)
}

func TestApplyUpdateAddsAndRemovesRegions(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	var otherCluster uuid.UUID
	addCluster := `
INSERT INTO clusters (name, region, provider, is_active, is_default, health_status)
VALUES ('c3', $1, 'kind', true, false, 'healthy') RETURNING id`
	if err := f.pool.QueryRow(context.Background(), addCluster, otherRegion).Scan(&otherCluster); err != nil {
		t.Fatalf("add cluster: %v", err)
	}
	applyOK(t, f, planFileHeader+applyFileOtherRegion, applyOptions{})

	resp := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{})
	started := resp.GetDeployments()
	if len(started) != 1 || started[0].GetRegion() != testRegion {
		t.Fatalf("deployments = %v, want one for the added region only", started)
	}
	if active := f.workerDeployments(t); len(active) != 2 {
		t.Fatalf("%d active deployments, want one per region", len(active))
	}
	wantCleanPlan(t, f, planFileHeader+applyFileTwoRegions)

	resp = applyOK(t, f, planFileHeader+applyFileOtherRegion, applyOptions{})
	if len(resp.GetDeployments()) != 0 {
		t.Fatalf("deployments = %v, want none for a removed region", resp.GetDeployments())
	}
	active := f.workerDeployments(t)
	if len(active) != 1 || active[0].Region != otherRegion {
		t.Fatalf("active deployments = %+v, want only %s", active, otherRegion)
	}
	res, _ := f.resourceByName(t, applyWorker)
	key := genDb.GetPlacementForResourceClusterParams{ResourceID: res.ID, ClusterID: f.clusterID}
	removed, err := f.queries.GetPlacementForResourceCluster(context.Background(), key)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if !removed.DesiredDeleted {
		t.Fatalf("placement in %s = %+v, want deleted", testRegion, removed)
	}
	wantCleanPlan(t, f, planFileHeader+applyFileOtherRegion)
}

func TestApplyStopsAndRestartsADisabledService(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	first := applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	firstDeployment := uuid.MustParse(first.GetDeployments()[0].GetDeploymentId())
	disabled := planFileHeader + applyFileWorker + "    environments:\n      prod:\n        enabled: false\n"

	_, err := applyFile(t, f, disabled, applyOptions{revision: f.revision(t, f.envID)}, f.adminScopes(t))
	if names := unconfirmedServices(wantRefusal(t, err)); len(names) != 1 || names[0] != applyWorker {
		t.Fatalf("unconfirmed = %v, want [%s]", names, applyWorker)
	}

	resp := applyOK(t, f, disabled, applyOptions{confirmDestructive: true})
	ops := resp.GetOperations()
	if len(ops) != 1 || ops[0].GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_UPDATE ||
		!ops[0].GetDestructive() || len(resp.GetDeployments()) != 0 {
		t.Fatalf("apply = %v, want one destructive update and no deployments", resp)
	}
	if status := f.deploymentStatus(t, firstDeployment); status != genDb.DeploymentStatusCanceled {
		t.Fatalf("deployment status = %s, want canceled", status)
	}
	res, found := f.resourceByName(t, applyWorker)
	if !found || derefString(res.Partial) != planPartial {
		t.Fatalf("resource = %+v found %v, want worker still owned by %s", res, found, planPartial)
	}
	key := genDb.GetPlacementForResourceClusterParams{ResourceID: res.ID, ClusterID: f.clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(context.Background(), key)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	if !placement.DesiredDeleted {
		t.Fatalf("placement = %+v, want deleted", placement)
	}
	wantCleanPlan(t, f, disabled)

	resp = applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	ops = resp.GetOperations()
	enabled := false
	for _, change := range ops[0].GetChanges() {
		enabled = enabled || change.GetPath() == "enabled" && change.GetAfter() == "true"
	}
	if len(ops) != 1 || !enabled || len(resp.GetDeployments()) != 1 {
		t.Fatalf("apply = %v, want an update enabling the service with one deployment", resp)
	}
	if active := f.workerDeployments(t); len(active) != 1 {
		t.Fatalf("%d active deployments, want 1", len(active))
	}
	wantCleanPlan(t, f, planFileHeader+applyFileWorker)
}

func TestApplyImportAppliesChanges(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.setRoutedSpec(t)
	f.addSucceededBuild(t, planSvc, testDockerfile, defaultBuildContext)
	file := planFileHeader + strings.Replace(planFileSvc, "100m", biggerCPU, 1)

	resp := applyOK(t, f, file, applyOptions{confirmImport: true})
	ops := resp.GetOperations()
	if len(ops) != 1 || ops[0].GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_IMPORT {
		t.Fatalf("operations = %v, want the import", ops)
	}
	if len(resp.GetDeployments()) != 1 {
		t.Fatalf("deployments = %v, want one on the latest build", resp.GetDeployments())
	}
	service := desiredPayload(t, f).AppSpec.ServiceSpec
	if service.Resources.CPU != biggerCPU || !strings.HasPrefix(service.Deployment.Image, testRegistryHost+"/svc@") {
		t.Fatalf("desired spec = %+v, want %s on the build image", service, biggerCPU)
	}
	wantCleanPlan(t, f, file)
}

func TestApplyUpdateThatNeedsADeployWritesConfigOnly(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileSource, applyOptions{})
	changed := strings.Replace(applyFileSource, "100m", biggerCPU, 1)

	resp := applyOK(t, f, planFileHeader+changed, applyOptions{})
	ops := resp.GetOperations()
	if len(ops) != 1 || !ops[0].GetNeedsDeploy() || len(resp.GetDeployments()) != 0 {
		t.Fatalf("apply = %v, want an update that needs a deploy and no deployments", resp)
	}
	res, _ := f.resourceByName(t, applyWorker)
	spec, err := converter.DeserializeResourceSpec(res.Spec, res.Type)
	if err != nil {
		t.Fatalf("resource spec: %v", err)
	}
	if cpu := spec.GetService().GetRegions()[testRegion].GetCpu(); cpu != biggerCPU {
		t.Fatalf("resource spec cpu = %s, want %s", cpu, biggerCPU)
	}
	if active := f.workerDeployments(t); len(active) != 0 {
		t.Fatalf("active deployments = %+v, want none before a loco deploy", active)
	}
}
