package apps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// budgetService is a service serving on two replicas behind the gateway, with a poller that
// records each request's outcome.
type budgetService struct {
	w          *platform.Workspace
	app        string
	dir        string
	file       locofile.File
	resourceID string
	selector   string
	oldHash    string
	deployment string
	poller     string
}

func newBudgetService(t *testing.T, app string) *budgetService {
	t.Helper()
	s := &budgetService{w: p.NewTenant(t), app: app, dir: t.TempDir(), poller: app + "-poller"}
	host := s.w.Host(app)
	service := platform.ImageService(p.PublicImage, host)
	service.Health = nil
	service.Regions = map[string]locofile.Region{p.Region: twoReplicas()}
	s.file = locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, s.dir, s.file)
	s.w.MustLoco(t, s.dir, "deploy", "--yes", "--wait")
	s.deployment = regionStatus(t, s.w, app).GetDeploymentId()
	s.resourceID = s.w.ResourceID(t, app)
	s.selector = "app=" + locov1alpha1.WorkloadName(s.resourceID)
	var sets appsv1.ReplicaSetList
	p.MustGet(t, &sets, "-n", s.w.Namespace, "replicasets", "-l", s.selector)
	if len(sets.Items) != 1 {
		t.Fatalf("%d ReplicaSets after the first rollout, want 1", len(sets.Items))
	}
	s.oldHash = sets.Items[0].Labels["pod-template-hash"]
	// Only a proven revision is brought back once its replicas are gone.
	platform.Eventually(t, 3*time.Minute, "the first version to prove itself", func(ctx context.Context) error {
		current, err := p.Application(ctx, s.resourceID, s.w.EnvironmentID)
		if err != nil {
			return err
		}
		revision, _ := current.PlacementRevision()
		if current.Status.Rollout == nil || current.Status.Rollout.ProvenRevision != revision {
			return fmt.Errorf("revision %d not proven", revision)
		}
		return nil
	})

	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(
		`wget -q -T 2 -O /dev/null --header "Host: %s" "http://%s.%s.svc.cluster.local/"`,
		host, gateway, p.Namespace,
	)
	p.StartPod(t, platform.ProbePod{Namespace: p.Namespace, Name: s.poller, Command: []string{
		"sh", "-c",
		fmt.Sprintf(`until %s; do sleep 1; done; while :; do if %s; then echo ok; else echo fail; fi; sleep 0.2; done`,
			request, request),
	}})
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", s.poller)
	platform.Eventually(t, 2*time.Minute, "requests to reach "+app, func(context.Context) error {
		if served := s.count(t, "ok"); served < 10 {
			return fmt.Errorf("%d requests served", served)
		}
		return nil
	})
	return s
}

// results lists the poller's request outcomes, oldest first.
func (s *budgetService) results(t *testing.T) []string {
	t.Helper()
	logs, err := p.Kubectl(t.Context(), "-n", p.Namespace, "logs", s.poller)
	if err != nil {
		t.Log(err)
	}
	return strings.Fields(logs)
}

func (s *budgetService) count(t *testing.T, result string) int {
	t.Helper()
	n := 0
	for _, outcome := range s.results(t) {
		if outcome == result {
			n++
		}
	}
	return n
}

// deploy rolls out image with a restart budget of one, set on the Application: the API's default
// budget takes minutes of kubelet back-off to use up.
func (s *budgetService) deploy(t *testing.T, image string) {
	t.Helper()
	s.file.Services[s.app] = withImage(s.file.Services[s.app], image)
	platform.WriteLocoFile(t, s.dir, s.file)
	s.w.MustLoco(t, s.dir, "deploy", "--yes")
	var application *locov1alpha1.Application
	platform.Eventually(t, 2*time.Minute, "the agent to apply "+image, func(ctx context.Context) error {
		current, err := p.Application(ctx, s.resourceID, s.w.EnvironmentID)
		if err != nil {
			return err
		}
		if current.Spec.DeploymentID == s.deployment {
			return fmt.Errorf("application still runs deployment %s", s.deployment)
		}
		application = current
		return nil
	})
	p.MustKubectl(t, "-n", application.Namespace, "patch", "applications", application.Name, "--type=merge",
		"-p", `{"spec":{"serviceSpec":{"restartPolicy":{"maxRestarts":1}}}}`)
}

// waitStopped waits for the controller to stop the new revision and checks what it recorded.
func (s *budgetService) waitStopped(t *testing.T) {
	t.Helper()
	var stopped *locov1alpha1.StoppedRevision
	platform.Eventually(t, 6*time.Minute, "the controller to stop the new revision", func(ctx context.Context) error {
		current, err := p.Application(ctx, s.resourceID, s.w.EnvironmentID)
		if err != nil {
			return err
		}
		status := current.Status.Rollout
		if status == nil || status.Stopped == nil {
			return errors.New("revision not stopped")
		}
		if status.Problem == nil || status.Problem.Reason != locov1alpha1.ProblemRestartsExhausted {
			return fmt.Errorf("problem %+v, want %s", status.Problem, locov1alpha1.ProblemRestartsExhausted)
		}
		stopped = status.Stopped
		return nil
	})
	if stopped.Restarts < 2 || stopped.LastRestart.Cause != locov1alpha1.RestartExit ||
		stopped.LastRestart.ExitCode != 1 || !stopped.Rollout {
		t.Errorf("stopped = %+v, want a rollout stopped after at least 2 restarts after exit 1", stopped)
	}
	var deployment appsv1.Deployment
	p.MustGet(t, &deployment, "-n", s.w.Namespace, "deployment", locov1alpha1.WorkloadName(s.resourceID))
	if !deployment.Spec.Paused {
		t.Error("the Deployment of the stopped revision is not paused")
	}
}

func (s *budgetService) oldReplicaSet(t *testing.T) appsv1.ReplicaSet {
	t.Helper()
	var sets appsv1.ReplicaSetList
	p.MustGet(t, &sets, "-n", s.w.Namespace, "replicasets", "-l", s.selector+",pod-template-hash="+s.oldHash)
	if len(sets.Items) != 1 {
		t.Fatalf("%d ReplicaSets of the old version, want 1", len(sets.Items))
	}
	return sets.Items[0]
}

// newPods names the pods of the new revision that are not shutting down.
func (s *budgetService) newPods(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, pod := range appPods(t, s.w, s.selector) {
		if pod.Labels["pod-template-hash"] != s.oldHash && pod.DeletionTimestamp == nil {
			names = append(names, pod.Name)
		}
	}
	return names
}

// holdsPreviousVersion checks for a minute that the stopped ReplicaSet stays empty and starts no
// pod, and that the previous one keeps two ready replicas.
func (s *budgetService) holdsPreviousVersion(t *testing.T) {
	t.Helper()
	before := s.newPods(t)
	samples := sampleReplicaSets(t, s.w.Namespace, s.selector)
	time.Sleep(time.Minute)
	recorded := samples.stop()
	newSamples, oldSamples := 0, 0
	for _, sample := range recorded {
		if sample.hash != s.oldHash {
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
	for _, name := range s.newPods(t) {
		if !slices.Contains(before, name) {
			t.Errorf("the stopped revision started pod %s", name)
		}
	}
}

// TestRestartBudgetStopsTheRevision deploys a version that exits over a serving one. Once a new
// replica restarts past its budget, the controller pauses the Deployment and empties the new
// ReplicaSet; the paused Deployment keeps the old ReplicaSet at full count and requests keep
// succeeding.
func TestRestartBudgetStopsTheRevision(t *testing.T) {
	t.Parallel()
	s := newBudgetService(t, "e2e-restart-budget")
	s.deploy(t, p.CrashingImage)
	s.waitStopped(t)
	servedBefore := s.count(t, "ok")
	s.holdsPreviousVersion(t)
	if served := s.count(t, "ok"); served <= servedBefore {
		t.Errorf("requests stopped reaching %s after the stop (%d served before, %d after)",
			s.app, servedBefore, served)
	}
	if failed := s.count(t, "fail"); failed != 0 {
		t.Errorf("%d requests failed across the stopped rollout", failed)
	}
}

// TestRestartBudgetBringsBackThePreviousVersion deploys a version that serves long enough to
// replace every old replica, then exits. Its stop scales the emptied old ReplicaSet back up, and
// requests succeed again.
func TestRestartBudgetBringsBackThePreviousVersion(t *testing.T) {
	t.Parallel()
	s := newBudgetService(t, "e2e-restart-fallback")
	s.deploy(t, p.LateCrashingImage)
	platform.Eventually(t, 2*time.Minute, "the new version to replace every old replica", func(context.Context) error {
		if replicas := s.oldReplicaSet(t).Spec.Replicas; replicas == nil || *replicas != 0 {
			return errors.New("the old ReplicaSet still has replicas")
		}
		return nil
	})
	s.waitStopped(t)
	platform.Eventually(t, 2*time.Minute, "the old version to serve again", func(context.Context) error {
		if ready := s.oldReplicaSet(t).Status.ReadyReplicas; ready != 2 {
			return fmt.Errorf("the old ReplicaSet has %d ready replicas, want 2", ready)
		}
		return nil
	})
	s.holdsPreviousVersion(t)
	const recent = 50
	if results := s.results(t); len(results) < recent || slices.Contains(results[len(results)-recent:], "fail") {
		t.Errorf("the last %d requests did not all succeed once the old version served again", recent)
	}
}
