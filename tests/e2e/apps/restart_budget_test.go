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

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
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
	s.waitProven(t)

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

// waitProven waits for the current revision to prove itself.
func (s *budgetService) waitProven(t *testing.T) {
	t.Helper()
	platform.Eventually(t, 3*time.Minute, "the revision to prove itself", func(ctx context.Context) error {
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

// deploy rolls out image with a budget of one restart.
func (s *budgetService) deploy(t *testing.T, image string) string {
	t.Helper()
	service := withImage(s.file.Services[s.app], image)
	service.Deploy = &locofile.Deploy{MaxRestarts: new(int32(1))}
	s.file.Services[s.app] = service
	platform.WriteLocoFile(t, s.dir, s.file)
	s.w.MustLoco(t, s.dir, "deploy", "--yes")
	deployment := regionStatus(t, s.w, s.app).GetDeploymentId()
	if deployment == s.deployment {
		t.Fatalf("the deploy of %s started no deployment", image)
	}
	return deployment
}

// waitDeploymentStatus waits for the API to report the deployment in status, with one event of
// eventType that counts the restarts.
func (s *budgetService) waitDeploymentStatus(
	t *testing.T,
	deployment string,
	status deploymentv1.DeploymentStatus,
	eventType string,
) {
	t.Helper()
	platform.Eventually(t, 2*time.Minute, "deployment "+status.String(), func(context.Context) error {
		if got := getDeployment(t, deployment).GetStatus(); got != status {
			return fmt.Errorf("deployment %s is %s", deployment, got)
		}
		return nil
	})
	var recorded []*eventv1.Event
	for _, event := range resourceEvents(t.Context(), t, s.w, s.resourceID, eventType) {
		if event.GetSubjectId() == deployment {
			recorded = append(recorded, event)
		}
	}
	if len(recorded) != 1 {
		t.Fatalf("%d %s events for deployment %s, want 1", len(recorded), eventType, deployment)
	}
	fields := recorded[0].GetData().GetFields()
	if restarts := fields["restarts"].GetNumberValue(); restarts < 2 {
		t.Errorf("%s data = %v, want at least 2 restarts", eventType, fields)
	}
}

// waitStopped waits for the controller to stop the current revision and checks what it recorded.
func (s *budgetService) waitStopped(t *testing.T, rollout bool, exitCode int32) {
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
	if stopped.Restarts < 2 || stopped.LastRestart == nil || stopped.LastRestart.Cause != locov1alpha1.RestartExit ||
		stopped.LastRestart.ExitCode != exitCode || stopped.Rollout != rollout {
		t.Errorf("stopped = %+v, want rollout %t after at least 2 restarts, the last after exit %d",
			stopped, rollout, exitCode)
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
	deployment := s.deploy(t, p.CrashingImage)
	s.waitStopped(t, true, 1)
	s.waitDeploymentStatus(t, deployment, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED, "deployment.failed")
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

// TestRestartBudgetBringsBackThePreviousVersion deploys a version that replaces every old replica,
// then makes it exit before it proves itself. Its stop scales the emptied old ReplicaSet back up,
// and requests succeed again.
func TestRestartBudgetBringsBackThePreviousVersion(t *testing.T) {
	t.Parallel()
	s := newBudgetService(t, "e2e-restart-fallback")
	deployment := s.deploy(t, p.LateCrashingImage)
	platform.Eventually(t, 2*time.Minute, "the new version to replace every old replica", func(context.Context) error {
		if replicas := s.oldReplicaSet(t).Spec.Replicas; replicas == nil || *replicas != 0 {
			return errors.New("the old ReplicaSet still has replicas")
		}
		return nil
	})
	for _, pod := range s.newPods(t) {
		p.MustKubectl(t, "-n", s.w.Namespace, "exec", pod, "--", "touch", "/dev/shm/crash")
	}
	s.waitStopped(t, true, 1)
	s.waitDeploymentStatus(t, deployment, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED, "deployment.failed")
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

// TestRestartBudgetKeepsAProvenServiceRunning restarts a service whose only version served for the
// health window past its budget. It reports the deployment crashed and keeps its replica.
func TestRestartBudgetKeepsAProvenServiceRunning(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-restart-crash"
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Health = nil
	service.Deploy = &locofile.Deploy{MaxRestarts: new(int32(1))}
	platform.WriteLocoFile(t, dir, locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	s := &budgetService{w: w, app: app, dir: dir, resourceID: w.ResourceID(t, app)}
	s.selector = "app=" + locov1alpha1.WorkloadName(s.resourceID)
	deployment := regionStatus(t, w, app).GetDeploymentId()
	s.waitProven(t)
	stop := func() {
		t.Helper()
		pods := appPods(t, w, s.selector)
		if len(pods) != 1 {
			t.Fatalf("%d pods, want 1", len(pods))
		}
		// The exec ends with the container it stops, killed with it.
		_, err := p.Kubectl(t.Context(), "-n", w.Namespace, "exec", pods[0].Name, "--", "nginx", "-s", "stop")
		if err != nil && !strings.Contains(err.Error(), "exit status 137") {
			t.Fatal(err)
		}
	}
	stop()
	platform.Eventually(t, 2*time.Minute, "the replica to run again after a restart", func(context.Context) error {
		for _, pod := range appPods(t, w, s.selector) {
			if statuses := pod.Status.ContainerStatuses; len(statuses) == 1 &&
				statuses[0].RestartCount == 1 && statuses[0].State.Running != nil {
				return nil
			}
		}
		return errors.New("not restarted")
	})
	stop()
	platform.Eventually(
		t,
		2*time.Minute,
		"the replica to report its used-up restarts",
		func(ctx context.Context) error {
			current, err := p.Application(ctx, s.resourceID, w.EnvironmentID)
			if err != nil {
				return err
			}
			if rollout := current.Status.Rollout; rollout == nil || rollout.Problem == nil ||
				rollout.Problem.Reason != locov1alpha1.ProblemRestartsExhausted {
				return errors.New("no restarts_exhausted problem")
			}
			if current.Status.Rollout.Stopped != nil {
				t.Fatalf("the proven revision was stopped: %+v", current.Status.Rollout.Stopped)
			}
			return nil
		},
	)
	platform.Eventually(t, 2*time.Minute, "the deployment to show crashed", func(context.Context) error {
		if got := getDeployment(
			t,
			deployment,
		).GetStatus(); got != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED {
			return fmt.Errorf("deployment is %s", got)
		}
		return nil
	})
	var running appsv1.Deployment
	p.MustGet(t, &running, "-n", w.Namespace, "deployment", locov1alpha1.WorkloadName(s.resourceID))
	if running.Spec.Paused || running.Spec.Replicas == nil || *running.Spec.Replicas != 1 {
		t.Errorf("the crashed Deployment is paused %t with %v replicas, want running with 1",
			running.Spec.Paused, running.Spec.Replicas)
	}

	platform.Eventually(t, 3*time.Minute, "the replica to recover", func(context.Context) error {
		if got := getDeployment(
			t,
			deployment,
		).GetStatus(); got != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
			return fmt.Errorf("deployment is %s", got)
		}
		return nil
	})
}

// TestLivenessKillIsNotACrash records a replica that fails its liveness check as restarted after
// failing its health check.
func TestLivenessKillIsNotACrash(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-restart-liveness"
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Health = &locofile.Health{
		Path: "/missing-health-check", Interval: new(int32(5)), Timeout: new(int32(1)), FailThreshold: new(int32(1)),
	}
	platform.WriteLocoFile(t, dir, locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}})
	w.MustLoco(t, dir, "deploy", "--yes")
	deployment := regionStatus(t, w, app).GetDeploymentId()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	const want = "Restarted after failing its health check"
	var seen []string
	stream, err := p.User.Deployment.WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: deployment},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		response, err := stream.Receive()
		if err != nil {
			t.Fatalf("no %q activity before the stream ended (%v); activity: %q", want, err, seen)
		}
		for _, entry := range response.GetActivity() {
			seen = append(seen, entry.GetMessage())
			if strings.HasPrefix(entry.GetMessage(), want) {
				return
			}
		}
	}
}
