package apps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestRestartBudgetStopsTheRevision deploys a version that exits over a serving one. Once a new
// replica restarts past its budget, the controller pauses the Deployment and empties the new
// ReplicaSet; the paused Deployment keeps the old ReplicaSet at full count and requests keep
// succeeding.
func TestRestartBudgetStopsTheRevision(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-restart-budget"
	host := w.Host(app)
	service := platform.ImageService(p.PublicImage, host)
	service.Health = nil
	service.Regions = map[string]locofile.Region{p.Region: twoReplicas()}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	oldDeployment := regionStatus(t, w, app).GetDeploymentId()
	resourceID := w.ResourceID(t, app)
	selector := "app=" + locov1alpha1.WorkloadName(resourceID)
	var sets appsv1.ReplicaSetList
	p.MustGet(t, &sets, "-n", w.Namespace, "replicasets", "-l", selector)
	if len(sets.Items) != 1 {
		t.Fatalf("%d ReplicaSets after the first rollout, want 1", len(sets.Items))
	}
	oldHash := sets.Items[0].Labels["pod-template-hash"]

	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const poller = "e2e-restart-budget-poller"
	request := fmt.Sprintf(
		`wget -q -T 2 -O /dev/null --header "Host: %s" "http://%s.%s.svc.cluster.local/"`,
		host, gateway, p.Namespace,
	)
	p.StartPod(t, platform.ProbePod{Namespace: p.Namespace, Name: poller, Command: []string{
		"sh", "-c",
		fmt.Sprintf(`until %s; do sleep 1; done; while :; do if %s; then echo ok; else echo fail; fi; sleep 0.2; done`,
			request, request),
	}})
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", poller)
	count := func(result string) int {
		logs, logErr := p.Kubectl(t.Context(), "-n", p.Namespace, "logs", poller)
		if logErr != nil {
			t.Log(logErr)
		}
		return strings.Count("\n"+logs, "\n"+result+"\n")
	}
	platform.Eventually(t, 2*time.Minute, "requests to reach "+app, func(context.Context) error {
		if served := count("ok"); served < 10 {
			return fmt.Errorf("%d requests served", served)
		}
		return nil
	})

	file.Services[app] = withImage(service, p.CrashingImage)
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	// The API's default budget takes minutes of kubelet back-off to use up; one restart keeps the test short.
	var application *locov1alpha1.Application
	platform.Eventually(t, 2*time.Minute, "the agent to apply the crashing version", func(ctx context.Context) error {
		current, getErr := p.Application(ctx, resourceID, w.EnvironmentID)
		if getErr != nil {
			return getErr
		}
		if current.Spec.DeploymentID == oldDeployment {
			return fmt.Errorf("application still runs deployment %s", oldDeployment)
		}
		application = current
		return nil
	})
	p.MustKubectl(t, "-n", application.Namespace, "patch", "applications", application.Name, "--type=merge",
		"-p", `{"spec":{"serviceSpec":{"restartPolicy":{"maxRestarts":1}}}}`)

	var stopped *locov1alpha1.StoppedRevision
	const stopping = "the controller to stop the crashing revision"
	platform.Eventually(t, 4*time.Minute, stopping, func(ctx context.Context) error {
		current, getErr := p.Application(ctx, resourceID, w.EnvironmentID)
		if getErr != nil {
			return getErr
		}
		rollout := current.Status.Rollout
		if rollout == nil || rollout.Stopped == nil {
			return errors.New("revision not stopped")
		}
		if rollout.Problem == nil || rollout.Problem.Reason != locov1alpha1.ProblemRestartsExhausted {
			return fmt.Errorf("problem %+v, want %s", rollout.Problem, locov1alpha1.ProblemRestartsExhausted)
		}
		stopped = rollout.Stopped
		return nil
	})
	if stopped.Restarts < 2 || stopped.LastRestart.Cause != locov1alpha1.RestartExit ||
		stopped.LastRestart.ExitCode != 1 {
		t.Errorf("stopped = %+v, want at least 2 restarts after exit 1", stopped)
	}
	var deployment appsv1.Deployment
	p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", locov1alpha1.WorkloadName(resourceID))
	if !deployment.Spec.Paused {
		t.Error("the Deployment of the stopped revision is not paused")
	}

	servedBefore := count("ok")
	samples := sampleReplicaSets(t, w.Namespace, selector)
	time.Sleep(time.Minute)
	recorded := samples.stop()
	newSamples, oldSamples := 0, 0
	for _, sample := range recorded {
		if sample.hash != oldHash {
			newSamples++
			if sample.replicas != 0 {
				t.Errorf("the stopped ReplicaSet %s wants %d replicas, want 0", sample.hash, sample.replicas)
			}
			continue
		}
		oldSamples++
		if sample.replicas != 2 || sample.ready != 2 {
			t.Errorf("the old ReplicaSet has %d desired, %d ready replicas, want 2", sample.replicas, sample.ready)
		}
	}
	if newSamples < 30 || oldSamples < 30 {
		t.Errorf("%d new and %d old ReplicaSet samples over a minute, want at least 30 each", newSamples, oldSamples)
	}
	if served := count("ok"); served <= servedBefore {
		t.Errorf("requests stopped reaching %s after the stop (%d served before, %d after)", app, servedBefore, served)
	}
	if failed := count("fail"); failed != 0 {
		t.Errorf("%d requests failed across the stopped rollout", failed)
	}
}
