package service

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

const (
	stagingWorkerDomain     = "worker-staging.loco.test"
	stagingPathPrefix       = "/staging"
	applyFileWorkerOverride = `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.loco.test]
    env: { LOG_LEVEL: info }
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
    environments:
      staging:
        routing: { pathPrefix: /staging }
        domains: [worker-staging.loco.test]
        env: { LOG_LEVEL: debug }
        regions:
          us-east-1: { cpu: 250m, replicas: { max: 2 } }
`
	applyFileWorkerMoreReplicas = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 3 } }
`
	applyFileWorkerSecondaries = `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.loco.test, z.loco.test, a.loco.test]
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	applyFileWorkerSecondariesReordered = `  worker:
    image: nginx:1.27
    port: 8080
    domains: [worker.loco.test, a.loco.test, z.loco.test]
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
)

func (f *deployFixture) prepareStaging(t *testing.T) uuid.UUID {
	t.Helper()
	stagingID := f.addStagingEnvironment(t)
	f.setClusterTier(t, f.otherCluster, stagingName)
	return stagingID
}

func wantCleanPlanIn(t *testing.T, f *deployFixture, environmentID uuid.UUID, file string) {
	t.Helper()
	again, err := planIn(t, f, environmentID, file, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(again.GetOperations()) != 0 || len(again.GetErrors()) != 0 {
		t.Fatalf("plan = %v, want no operations", again)
	}
}

func TestApplyKeepsTheValuesOfEachEnvironment(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	stagingID := f.prepareStaging(t)
	file := planFileHeader + applyFileWorkerOverride

	applyOK(t, f, file, applyOptions{})
	applyOK(t, f, file, applyOptions{environmentID: stagingID})

	if got := len(f.activeDeployments(t, f.envID)); got != 1 {
		t.Fatalf("production active deployments = %d, want 1", got)
	}
	if got := len(f.activeDeployments(t, stagingID)); got != 1 {
		t.Fatalf("staging active deployments = %d, want 1", got)
	}
	production := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec
	staging := f.workerPayload(t, f.otherCluster).AppSpec.ServiceSpec
	if production.Resources.CPU != testRegionCPU || production.Deployment.Env["LOG_LEVEL"] != "info" ||
		production.Routing.HostName != applyWorkerDomain || production.Routing.PathPrefix == stagingPathPrefix {
		t.Fatalf("production spec = %+v, want the base values", production)
	}
	if staging.Resources.CPU != biggerCPU || staging.Resources.Replicas.Max != 2 ||
		staging.Deployment.Env["LOG_LEVEL"] != "debug" || staging.Routing.HostName != stagingWorkerDomain ||
		staging.Routing.PathPrefix != stagingPathPrefix {
		t.Fatalf("staging spec = %+v, want the staging overrides", staging)
	}
	wantCleanPlanIn(t, f, f.envID, file)
	wantCleanPlanIn(t, f, stagingID, file)

	applyOK(t, f, file, applyOptions{})
	if got := len(f.activeDeployments(t, stagingID)); got != 1 {
		t.Fatalf("staging active deployments = %d after a production re-apply, want 1", got)
	}
	wantCleanPlanIn(t, f, stagingID, file)
}

func TestDisablingAServiceInOneEnvironmentKeepsTheOther(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	stagingID := f.prepareStaging(t)
	file := planFileHeader + applyFileWorkerOverride
	applyOK(t, f, file, applyOptions{})
	applyOK(t, f, file, applyOptions{environmentID: stagingID})

	disabled := file + "        enabled: false\n"
	applyOK(t, f, disabled, applyOptions{environmentID: stagingID, confirmDestructive: true})
	if got := len(f.activeDeployments(t, stagingID)); got != 0 {
		t.Fatalf("staging active deployments = %d, want 0", got)
	}
	if got := len(f.activeDeployments(t, f.envID)); got != 1 {
		t.Fatalf("production active deployments = %d after staging disabled it, want 1", got)
	}
	wantCleanPlanIn(t, f, f.envID, disabled)
}

func TestApplyConvergesWhenOnlySecondaryDomainsMove(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorkerSecondaries, applyOptions{})
	wantCleanPlan(t, f, planFileHeader+applyFileWorkerSecondariesReordered)
}

func TestApplyInTwoEnvironmentsDoNotDeadlock(t *testing.T) {
	for range interleavingRuns {
		f := newDeployFixture(t)
		f.prepareApply(t)
		stagingID := f.prepareStaging(t)
		applyOK(t, f, planFileHeader+applyFileWorkerOverride, applyOptions{})
		applyOK(t, f, planFileHeader+applyFileWorkerOverride, applyOptions{environmentID: stagingID})
		worker, _ := f.resourceByName(t, applyWorker)
		f.resourceID = worker.ID
		f.useLockTimeout(t)
		release := f.holdResourceLock(t)

		file := planFileHeader + applyFileWorkerMoreReplicas
		var wg sync.WaitGroup
		errs := make([]error, 0, writersPerInterleaving)
		var mu sync.Mutex
		for _, environmentID := range []uuid.UUID{f.envID, stagingID} {
			opts := applyOptions{revision: f.revision(t, environmentID), environmentID: environmentID}
			wg.Go(func() {
				_, err := applyFile(t, f, file, opts, f.adminScopes(t))
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			})
		}
		f.waitForLockWaiters(t, writersPerInterleaving)
		release()
		wg.Wait()

		for _, err := range errs {
			if err != nil && connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("Apply = %v, want success or a stale-revision refusal", err)
			}
		}
		active, err := f.queries.ListActiveDeploymentsForResource(context.Background(), worker.ID)
		if err != nil {
			t.Fatalf("list deployments: %v", err)
		}
		environments := map[uuid.UUID]bool{}
		for _, deployment := range active {
			environments[deployment.EnvironmentID] = true
		}
		if !environments[f.envID] || !environments[stagingID] {
			t.Fatalf("active deployments = %+v, want both environments running", active)
		}
	}
}
