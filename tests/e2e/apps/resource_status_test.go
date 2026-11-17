package apps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	statusTimeout     = 3 * time.Minute
	revisionTimeout   = time.Minute
	suspensionReason  = "e2e suspension"
	rolloutRejection  = "a deployment is rolling out; wait for it to finish before changing this service"
	placementRevision = "loco.io/placement-revision"
)

// TestResourceStatus follows what the API reports about a resource: healthy after a deploy and a
// restart, unavailable while its pods cannot run, unknown while its cluster is silent, mutations
// refused during a rollout, stopped while its organization is suspended, one crash-loop event
// across an agent reconnect, a failed rollout that keeps the old version serving, and a worker
// without a port. It cordons the node and stops the agent, so it runs before the parallel tests.
func TestResourceStatus(t *testing.T) {
	w := p.NewTenant(t)
	const app = statusApp
	dir := t.TempDir()
	s := &status{w: w, dir: dir}
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{Partial: app, Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)}},
	)

	t.Run("a deploy reports healthy", s.deployReportsHealthy)

	s.id = w.ResourceID(t, app)
	s.selector = "app=resource-" + s.id

	t.Run("a restart keeps the deployment", s.restartKeepsDeployment)

	t.Run("pods that cannot run report unavailable", s.unavailable)

	t.Run("a silent cluster reports unknown", func(t *testing.T) {
		p.StopAgent()
		waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN)
		if err := p.StartAgent(t.Context(), p.APIURL, ""); err != nil {
			t.Fatal(err)
		}
		waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
	})

	t.Run("a rollout rejects runtime mutations", s.rolloutRejectsMutations)

	t.Run("suspension stops pods and resume restores them", s.suspension)

	t.Run("a crash loop records one event across a reconnect", s.crashLoopEvent)

	t.Run("a failed rollout keeps the old version serving", s.failedRolloutKeepsServing)

	t.Run("a worker without a port runs", func(t *testing.T) {
		const worker = "e2e-worker"
		workerDir := t.TempDir()
		platform.WriteLocoFile(t, workerDir, locofile.File{Partial: worker, Services: map[string]locofile.Service{
			worker: {Image: p.WorkerImage, Regions: map[string]locofile.Region{p.Region: platform.OneReplica()}},
		}})
		if out := w.MustLoco(
			t,
			workerDir,
			"deploy",
			"--yes",
			"--wait",
		).Stdout; !strings.Contains(
			out,
			"has no port, so it runs as a worker",
		) {
			t.Errorf("loco deploy does not say the worker takes no traffic:\n%s", out)
		}
		workerID := w.ResourceID(t, worker)
		for _, pod := range appPods(t, w, "app=resource-"+workerID) {
			if pod.Spec.Containers[0].ReadinessProbe != nil {
				t.Errorf("the worker's pod %s has a readiness probe", pod.Name)
			}
		}
		for _, name := range []string{"resource-" + workerID, worker} {
			if found, err := p.Exists(t.Context(), "-n", w.Namespace, "service", name); err != nil || found {
				t.Errorf("the worker has Service %s (%v)", name, err)
			}
		}
	})
}

// statusApp is the app most of TestResourceStatus follows.
const statusApp = "e2e-status"

// status is the app TestResourceStatus follows and where its loco.yaml is.
type status struct {
	w        *platform.Workspace
	dir      string
	id       string
	selector string
}

// failedRolloutKeepsServing deploys two replicas behind the gateway, then a version that exits
// without listening: the rollout fails, requests keep succeeding, and the old ReplicaSet keeps
// both ready replicas.
func (s *status) failedRolloutKeepsServing(t *testing.T) {
	w := s.w
	const app = "e2e-rollout"
	host := platform.Host(app)
	dir := t.TempDir()
	// No health check: the next version runs for a few seconds without listening and exits, and
	// must not displace any old replica in that time.
	service := platform.ImageService(p.PublicImage, host)
	service.Health = nil
	service.Regions = map[string]locofile.Region{
		p.Region: {CPU: "100m", Memory: "128Mi", Replicas: &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))}},
	}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	twoReady := func(ctx context.Context) error {
		current, err := statusOf(ctx, w, app)
		if err != nil {
			return err
		}
		if current.status != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY ||
			current.region.GetReadyReplicas() != 2 ||
			current.region.GetDesiredReplicas() != 2 {
			return fmt.Errorf(
				"%v with %d of %d ready",
				current.status,
				current.region.GetReadyReplicas(),
				current.region.GetDesiredReplicas(),
			)
		}
		return nil
	}
	platform.Eventually(
		t,
		statusTimeout,
		app+" to finish its first rollout on two replicas",
		func(ctx context.Context) error {
			if err := twoReady(ctx); err != nil {
				return err
			}
			if region := regionStatus(
				t,
				w,
				app,
			); region.GetDeploymentStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
				return fmt.Errorf("deployment %v", region.GetDeploymentStatus())
			}
			return nil
		},
	)
	oldDeployment := regionStatus(t, w, app).GetDeploymentId()
	id := w.ResourceID(t, app)
	selector := "app=resource-" + id
	var replicaSets appsv1.ReplicaSetList
	p.MustGet(t, &replicaSets, "-n", w.Namespace, "replicasets", "-l", selector)
	oldHash := replicaSets.Items[0].Labels["pod-template-hash"]

	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const poller = "e2e-rollout-poller"
	p.StartPod(t, platform.ProbePod{Namespace: p.Namespace, Name: poller, Command: []string{
		"sh",
		"-c",
		fmt.Sprintf(
			`while :; do if wget -q -T 2 -O /dev/null --header "Host: %s" "http://%s.%s.svc.cluster.local/"; `+
				`then echo ok; else echo fail; fi; sleep 0.2; done`,
			host,
			gateway,
			p.Namespace,
		),
	}})
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", poller)
	count := func(result string) int {
		logs, err := p.Kubectl(t.Context(), "-n", p.Namespace, "logs", poller)
		if err != nil {
			t.Log(err)
		}
		return strings.Count("\n"+logs, "\n"+result+"\n")
	}
	platform.Eventually(t, 2*time.Minute, "requests through the gateway to reach "+app, func(context.Context) error {
		if served := count("ok"); served < 10 {
			return fmt.Errorf("%d requests served", served)
		}
		return nil
	})
	servedBefore := count("ok")
	samples := sampleReplicaSets(t, w.Namespace, selector)

	file.Services[app] = withImage(file.Services[app], p.CrashingImage)
	platform.WriteLocoFile(t, dir, file)
	crashing := w.Loco(t.Context(), dir, nil, "deploy", "--yes", "--wait")
	newDeployment := regionStatus(t, w, app).GetDeploymentId()
	failure := fmt.Sprintf("deployment %s for %s in %s failed", newDeployment, app, p.Region)
	if crashing.Err == nil || !strings.Contains(strings.Join(strings.Fields(crashing.Output()), " "), failure) {
		t.Errorf("loco deploy --wait of the crashing version: %v, want an error naming %q:\n%s",
			crashing.Err, failure, crashing.Output())
	}
	restarts, _ := newPodState(t, w, selector, oldHash)
	// A replica that counted as available during another run would replace an old one.
	platform.Eventually(t, 2*time.Minute, "the crashing version to start and exit again", func(context.Context) error {
		if now, running := newPodState(t, w, selector, oldHash); now <= restarts || running {
			return fmt.Errorf("%d restarts, running %v", now, running)
		}
		return nil
	})
	recorded := samples.stop()
	served, failed := count("ok"), count("fail")
	if served <= servedBefore {
		t.Errorf(
			"requests stopped reaching the app during the failed rollout (%d served before, %d after)",
			servedBefore,
			served,
		)
	}
	if failed != 0 {
		t.Errorf("%d requests failed during the failed rollout", failed)
	}
	newSamples := 0
	for _, sample := range recorded {
		if sample.hash != oldHash {
			newSamples++
			if sample.replicas > 1 {
				t.Errorf("the rollout started %d new replicas, want at most one", sample.replicas)
			}
		} else if sample.replicas < 2 || sample.ready < 2 {
			t.Errorf("the old ReplicaSet dropped to %d desired, %d ready replicas", sample.replicas, sample.ready)
		}
	}
	if newSamples < 10 {
		t.Errorf("%d replica samples cover the new ReplicaSet, want at least 10", newSamples)
	}
	newPods := 0
	for _, pod := range appPods(t, w, selector) {
		if pod.Labels["pod-template-hash"] != oldHash {
			newPods++
		}
	}
	if newPods > 1 {
		t.Errorf("%d replicas of the new version exist, want at most one", newPods)
	}
	if status := getDeployment(
		t,
		newDeployment,
	).GetStatus(); status != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		t.Errorf("the API reports the new deployment %v, want failed", status)
	}
	checkDeploymentFailedEvent(t, w, id, newDeployment, app)
	if err := twoReady(t.Context()); err != nil {
		t.Errorf("the API does not report the app healthy while the old replicas serve: %v", err)
	}
	// The API still treats the failed deployment as active and the serving one as replaced;
	// https://github.com/team-loco/loco/issues/1774 tracks reporting the serving deployment.
	if !getDeployment(t, newDeployment).GetIsActive() {
		t.Error("the failed deployment is not the active one")
	}
	if getDeployment(t, oldDeployment).GetIsActive() {
		t.Error("the serving deployment is reported active")
	}
}

// checkDeploymentFailedEvent checks one deployment.failed event names the deployment, its region,
// its resource and creator, and the user who deployed it.
func checkDeploymentFailedEvent(t *testing.T, w *platform.Workspace, resourceID, deploymentID, app string) {
	t.Helper()
	failedEvents := slices.DeleteFunc(
		resourceEvents(t.Context(), t, w, resourceID, "deployment.failed"),
		func(event *eventv1.Event) bool { return event.GetSubjectId() != deploymentID },
	)
	if len(failedEvents) != 1 {
		t.Fatalf("%d deployment.failed events name deployment %s, want one", len(failedEvents), deploymentID)
	}
	data := failedEvents[0].GetData().GetFields()
	if region := data["region"].GetStringValue(); region != p.Region {
		t.Errorf("the deployment.failed event names region %q, want %q", region, p.Region)
	}
	if name := data["resource_name"].GetStringValue(); name != app {
		t.Errorf("the deployment.failed event names resource %q, want %q", name, app)
	}
	if data["creator_email"].GetStringValue() == "" {
		t.Error("the deployment.failed event names no creator")
	}
	if actor := failedEvents[0].GetActorType(); actor != "user" {
		t.Errorf("the deployment.failed event's actor is %q, want user", actor)
	}
}

func withImage(service locofile.Service, image string) locofile.Service {
	service.Image = image
	return service
}

type replicaSample struct {
	hash            string
	replicas, ready int32
}

type sampler struct {
	stopped chan struct{}
	done    sync.WaitGroup
	samples []replicaSample
}

// sampleReplicaSets records each second the desired and ready replicas of every ReplicaSet the
// selector matches, until stop.
func sampleReplicaSets(t *testing.T, namespace, selector string) *sampler {
	t.Helper()
	s := &sampler{stopped: make(chan struct{})}
	s.done.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var list appsv1.ReplicaSetList
			if err := p.Get(t.Context(), &list, "-n", namespace, "replicasets", "-l", selector); err == nil {
				for _, set := range list.Items {
					replicas := int32(0)
					if set.Spec.Replicas != nil {
						replicas = *set.Spec.Replicas
					}
					s.samples = append(
						s.samples,
						replicaSample{set.Labels["pod-template-hash"], replicas, set.Status.ReadyReplicas},
					)
				}
			}
			select {
			case <-s.stopped:
				return
			case <-ticker.C:
			}
		}
	})
	t.Cleanup(func() { s.stop() })
	return s
}

func (s *sampler) stop() []replicaSample {
	select {
	case <-s.stopped:
	default:
		close(s.stopped)
	}
	s.done.Wait()
	return s.samples
}

// newPodState returns the restart count of the new version's pod and whether its container runs.
func newPodState(t *testing.T, w *platform.Workspace, selector, oldHash string) (int32, bool) {
	t.Helper()
	for _, pod := range appPods(t, w, selector) {
		if pod.Labels["pod-template-hash"] != oldHash && len(pod.Status.ContainerStatuses) > 0 {
			status := pod.Status.ContainerStatuses[0]
			return status.RestartCount, status.State.Running != nil
		}
	}
	return 0, false
}

func appPods(t *testing.T, w *platform.Workspace, selector string) []corev1.Pod {
	t.Helper()
	var pods corev1.PodList
	p.MustGet(t, &pods, "-n", w.Namespace, "pods", "-l", selector)
	return pods.Items
}

func getDeployment(t *testing.T, id string) *deploymentv1.Deployment {
	t.Helper()
	response, err := p.User.Deployment.GetDeployment(t.Context(), &deploymentv1.GetDeploymentRequest{DeploymentId: id})
	if err != nil {
		t.Fatal(err)
	}
	return response.GetDeployment()
}

// appStatus is the resource's status in its one environment, and its one region's status there.
type appStatus struct {
	status resourcev1.ResourceStatus
	region *resourcev1.RegionStatus
}

func statusOf(ctx context.Context, w *platform.Workspace, app string) (appStatus, error) {
	resource, err := w.Resource(ctx, app)
	if err != nil {
		return appStatus{}, err
	}
	environments := resource.GetEnvironmentStatuses()
	if len(environments) == 0 || len(environments[0].GetRegions()) == 0 {
		return appStatus{}, fmt.Errorf("%s reports no region status", app)
	}
	return appStatus{status: environments[0].GetStatus(), region: environments[0].GetRegions()[0]}, nil
}

func regionStatus(t *testing.T, w *platform.Workspace, app string) *resourcev1.RegionStatus {
	t.Helper()
	status, err := statusOf(t.Context(), w, app)
	if err != nil {
		t.Fatal(err)
	}
	return status.region
}

func waitStatus(t *testing.T, w *platform.Workspace, want resourcev1.ResourceStatus) {
	t.Helper()
	const app = statusApp
	platform.Eventually(
		t,
		statusTimeout,
		fmt.Sprintf("the API to report %s %v", app, want),
		func(ctx context.Context) error {
			current, err := statusOf(ctx, w, app)
			if err != nil {
				return err
			}
			if current.status != want {
				return fmt.Errorf("status %v", current.status)
			}
			return nil
		},
	)
}

func waitRolloutFinished(t *testing.T, w *platform.Workspace, app string) {
	t.Helper()
	platform.Eventually(t, statusTimeout, app+"'s rollout to finish", func(ctx context.Context) error {
		current, err := statusOf(ctx, w, app)
		if err != nil {
			return err
		}
		if current.region.GetDeploymentStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING ||
			current.status != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY {
			return fmt.Errorf("deployment %v, status %v", current.region.GetDeploymentStatus(), current.status)
		}
		return nil
	})
}

func templateRevision(t *testing.T, w *platform.Workspace, selector string) string {
	t.Helper()
	var deployments appsv1.DeploymentList
	p.MustGet(t, &deployments, "-n", w.Namespace, "deployments", "-l", selector)
	if len(deployments.Items) == 0 {
		t.Fatalf("no Deployment matches %s", selector)
	}
	return deployments.Items[0].Spec.Template.Annotations[placementRevision]
}

func deploymentCount(t *testing.T, resourceID string) int {
	t.Helper()
	var count int
	if err := p.DB.QueryRow(t.Context(), "SELECT count(*) FROM deployments WHERE resource_id = $1", resourceID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func requireRolloutRejection(t *testing.T, result platform.CLIResult, what string) {
	t.Helper()
	if result.Err == nil {
		t.Errorf("%s during a rollout was accepted:\n%s", what, result.Output())
		return
	}
	if !strings.Contains(strings.Join(strings.Fields(result.Output()), " "), rolloutRejection) {
		t.Errorf("%s during a rollout was refused without %q:\n%s", what, rolloutRejection, result.Output())
	}
}

// withoutScope runs check while the CLI has no cached organization or workspace.
func withoutScope(t *testing.T, w *platform.Workspace, check func()) {
	t.Helper()
	config := w.ConfigPath()
	if err := os.Rename(config, config+".saved"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(config+".saved", config); err != nil {
			t.Error(err)
		}
	}()
	check()
}

func crashEvents(ctx context.Context, t *testing.T, w *platform.Workspace, resourceID string) []*eventv1.Event {
	t.Helper()
	var crashes []*eventv1.Event
	for _, event := range resourceEvents(ctx, t, w, resourceID, "resource.degraded") {
		if event.GetData().GetFields()["reason"].GetStringValue() == "crash_loop" {
			crashes = append(crashes, event)
		}
	}
	return crashes
}

func eventIDs(t *testing.T, w *platform.Workspace, resourceID string) []string {
	t.Helper()
	events := resourceEvents(t.Context(), t, w, resourceID, "resource.degraded")
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.GetId())
	}
	slices.Sort(ids)
	return ids
}

func resourceEvents(
	ctx context.Context,
	t *testing.T,
	w *platform.Workspace,
	resourceID, eventType string,
) []*eventv1.Event {
	t.Helper()
	listed, err := p.User.Event.ListEvents(ctx, &eventv1.ListEventsRequest{
		OrgId:         w.OrgID,
		WorkspaceId:   w.ID,
		ResourceId:    resourceID,
		EnvironmentId: w.EnvironmentID,
		Query:         "@type:" + eventType,
	})
	if err != nil {
		t.Fatal(err)
	}
	return listed.GetEvents()
}

func dumpPlacement(t *testing.T, w *platform.Workspace, resourceID string) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	rows, err := p.DB.Query(
		ctx,
		`SELECT p.desired_revision, p.applied_revision, p.ready_replicas, p.status_updated_at, ro.snapshot
 FROM placements p LEFT JOIN deployment_rollouts ro
 ON ro.deployment_id = p.deployment_id AND ro.revision = p.observed_revision
 WHERE p.resource_id = $1`,
		resourceID,
	)
	if err == nil {
		for rows.Next() {
			values, err := rows.Values()
			t.Logf("placement: %v %v", values, err)
		}
		rows.Close()
	}
	for _, args := range [][]string{
		{"-n", p.Namespace, "get", "application", "resource-" + resourceID, "-o", "jsonpath={.status}"},
		{"-n", w.Namespace, "get", "pods", "-l", "app=resource-" + resourceID, "-o", "yaml"},
	} {
		out, err := p.Kubectl(ctx, args...)
		t.Logf("kubectl %s:\n%s%v", strings.Join(args, " "), out, err)
	}
}

func (s *status) deployReportsHealthy(t *testing.T) {
	const app = statusApp
	w, dir := s.w, s.dir
	w.MustLoco(t, dir, "deploy", "--yes")
	waitRolloutFinished(t, w, app)
	resource, err := w.Resource(t.Context(), app)
	if err != nil {
		t.Fatal(err)
	}
	if got := resource.GetCreatedBy(); got != p.UserID {
		t.Errorf("resource creator = %q, want %s", got, p.UserID)
	}
	region := regionStatus(t, w, app)
	ready := resourceEvents(t.Context(), t, w, w.ResourceID(t, app), "deployment.ready")
	if len(ready) != 1 || ready[0].GetSubjectId() != region.GetDeploymentId() {
		t.Fatalf("deployment.ready events = %v, want the running deployment", ready)
	}
	if region.GetReadyReplicas() != 1 || region.GetDesiredReplicas() != 1 {
		t.Errorf(
			"region reports %d of %d replicas ready, want 1 of 1",
			region.GetReadyReplicas(),
			region.GetDesiredReplicas(),
		)
	}
	if out := w.MustLoco(t, dir, "resource", "status", app).Stdout; !strings.Contains(out, "Healthy") {
		t.Errorf("the table view does not show the environment healthy:\n%s", out)
	}
	listed, err := p.User.Deployment.ListDeployments(
		t.Context(),
		&deploymentv1.ListDeploymentsRequest{WorkspaceId: w.ID, EnvironmentId: w.EnvironmentID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetDeployments()) == 0 {
		t.Error("the environment's deployment list omits the running resource")
	}
	for _, deployment := range listed.GetDeployments() {
		started, created := deployment.GetStartedAt(), deployment.GetCreatedAt()
		if started == nil || started.AsTime().Before(created.AsTime()) {
			t.Errorf("deployment start = %v, created = %v", started, created)
		}
		if deployment.GetEnvironmentId() != w.EnvironmentID {
			t.Errorf(
				"the environment's deployment list holds a deployment of environment %s",
				deployment.GetEnvironmentId(),
			)
		}
	}
	withoutScope(t, w, func() {
		out := w.MustLoco(t, dir, "resource", "status", app, "--org", w.OrgName, "--workspace", w.Name).Stdout
		if !strings.Contains(out, "Healthy") {
			t.Errorf("a workspace member resolving the workspace by name sees no Healthy status:\n%s", out)
		}
	})
}

func (s *status) restartKeepsDeployment(t *testing.T) {
	const app = statusApp
	w, dir, id, selector := s.w, s.dir, s.id, s.selector
	// A replica reports ready before it has stayed ready long enough to finish the rollout.
	waitRolloutFinished(t, w, app)
	before := templateRevision(t, w, selector)
	deploymentID := regionStatus(t, w, app).GetDeploymentId()
	count := deploymentCount(t, id)
	w.MustLoco(t, dir, "resource", "restart", app, "--env", platform.Production)
	platform.Eventually(
		t,
		revisionTimeout,
		"the restart revision to reach the pod template",
		func(context.Context) error {
			if revision := templateRevision(t, w, selector); revision == before {
				return fmt.Errorf("revision still %s", revision)
			}
			return nil
		},
	)
	p.MustKubectl(t, "-n", w.Namespace, "rollout", "status", "deployment", "-l", selector, "--timeout=180s")
	waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
	if got := regionStatus(t, w, app).GetDeploymentId(); got != deploymentID {
		t.Errorf("the restart replaced deployment %s with %s", deploymentID, got)
	}
	if got := deploymentCount(t, id); got != count {
		t.Errorf("the restart created %d deployment records", got-count)
	}
	if out := w.MustLoco(
		t,
		dir,
		"resource",
		"events",
		app,
		"--output",
		"json",
	).Stdout; !strings.Contains(
		out,
		"resource.restarted",
	) {
		t.Errorf("the resource events lack resource.restarted:\n%s", out)
	}
}

func (s *status) unavailable(t *testing.T) {
	const app = statusApp
	w, selector := s.w, s.selector
	p.MustKubectl(t, "cordon", p.Node())
	t.Cleanup(func() {
		if _, err := p.Kubectl(context.WithoutCancel(t.Context()), "uncordon", p.Node()); err != nil {
			t.Error(err)
		}
	})
	p.MustKubectl(t, "-n", w.Namespace, "delete", "pods", "-l", selector, "--wait=false")
	waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE)
	region := regionStatus(t, w, app)
	p.MustKubectl(t, "uncordon", p.Node())
	if region.GetDeploymentStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING {
		t.Errorf("the deployment reads %v while its pods cannot run, want running", region.GetDeploymentStatus())
	}
	if region.GetReadyReplicas() != 0 {
		t.Errorf("the region reports %d ready replicas, want none", region.GetReadyReplicas())
	}
	waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
}

func (s *status) rolloutRejectsMutations(t *testing.T) {
	const app = statusApp
	w, dir, id := s.w, s.dir, s.id
	if err := p.SignalAgent(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	resumed := false
	resume := func() {
		if !resumed {
			resumed = true
			if err := p.SignalAgent(syscall.SIGCONT); err != nil {
				t.Error(err)
			}
		}
	}
	defer resume()
	w.MustLoco(t, dir, "resource", "scale", app, "--cpu", "125m")
	requireRolloutRejection(t, w.Loco(t.Context(), dir, nil, "resource", "scale", app, "--cpu", "150m"), "scaling")
	requireRolloutRejection(
		t,
		w.Loco(
			t.Context(),
			dir,
			nil,
			"env",
			"set",
			"--service",
			app,
			"--env",
			platform.Production,
			"ROLLOUT_TEST=changed",
		),
		"a secret edit",
	)
	_, err := p.User.Deployment.CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{
		ResourceId:    id,
		EnvironmentId: w.EnvironmentID,
		Region:        p.Region,
		Spec: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Build: &deploymentv1.BuildSource{Type: "image", Image: p.PublicImage},
				Port:  new(int32(8080)),
				Env:   map[string]string{"ROLLOUT_TEST": "changed"},
			}},
		},
	})
	if err == nil || !strings.Contains(strings.Join(strings.Fields(err.Error()), " "), rolloutRejection) {
		t.Errorf("a replacement deployment during a rollout: %v, want %q", err, rolloutRejection)
	}
	resume()
	waitRolloutFinished(t, w, app)
	w.MustLoco(t, dir, "resource", "scale", app, "--cpu", "150m")
	waitRolloutFinished(t, w, app)
}

func (s *status) suspension(t *testing.T) {
	const app = statusApp
	w, dir, selector := s.w, s.dir, s.selector
	_, err := p.User.Org.SuspendOrg(t.Context(), &orgv1.SuspendOrgRequest{OrgId: w.OrgID, Reason: suspensionReason})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Errorf(
			"an organization member suspending the organization: code %v (%v), want permission_denied",
			code,
			err,
		)
	}
	suspended, err := p.Admin.Org.SuspendOrg(
		t.Context(),
		&orgv1.SuspendOrgRequest{OrgId: w.OrgID, Reason: suspensionReason},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(suspended.String(), suspensionReason) {
		t.Errorf("the suspended organization %v does not carry the reason", suspended)
	}
	platform.Eventually(t, 2*time.Minute, "the suspended app's pods to stop", func(context.Context) error {
		if pods := appPods(t, w, selector); len(pods) != 0 {
			return fmt.Errorf("%d pods", len(pods))
		}
		return nil
	})
	if result := w.Loco(
		t.Context(),
		dir,
		nil,
		"resource",
		"scale",
		app,
		"--cpu",
		"175m",
	); result.Err == nil ||
		!strings.Contains(result.Output(), suspensionReason) {
		t.Errorf("scaling while suspended: %v\n%s", result.Err, result.Output())
	}
	if out := w.MustLoco(t, dir, "resource", "status", app).Stdout; !strings.Contains(out, app) {
		t.Errorf("the service does not read while suspended:\n%s", out)
	}
	if _, err := p.Admin.Org.ResumeOrg(t.Context(), &orgv1.ResumeOrgRequest{OrgId: w.OrgID}); err != nil {
		t.Fatal(err)
	}
	platform.Eventually(t, statusTimeout, "the resumed app's pod to run", func(context.Context) error {
		for _, pod := range appPods(t, w, selector) {
			if pod.Status.Phase == corev1.PodRunning {
				return nil
			}
		}
		return errors.New("no running pod")
	})
	waitStatus(t, w, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
	w.MustLoco(t, dir, "resource", "scale", app, "--cpu", "175m")
	waitRolloutFinished(t, w, app)
}

func (s *status) crashLoopEvent(t *testing.T) {
	w := s.w
	const crashing = "e2e-runtime-crash"
	crashDir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Health = &locofile.Health{
		Path:          "/missing-health-check",
		Interval:      new(int32(5)),
		Timeout:       new(int32(1)),
		FailThreshold: new(int32(1)),
	}
	platform.WriteLocoFile(
		t,
		crashDir,
		locofile.File{Partial: crashing, Services: map[string]locofile.Service{crashing: service}},
	)
	// Deployment acceptance succeeds before the runtime failure is observed.
	w.MustLoco(t, crashDir, "deploy", "--yes")
	crashID := w.ResourceID(t, crashing)
	t.Cleanup(func() {
		if t.Failed() {
			dumpPlacement(t, w, crashID)
		}
	})
	platform.Eventually(
		t,
		statusTimeout,
		"one crash-loop event through ListEvents",
		func(ctx context.Context) error {
			if events := crashEvents(ctx, t, w, crashID); len(events) != 1 {
				return fmt.Errorf("%d crash-loop events", len(events))
			}
			return nil
		},
	)
	before := eventIDs(t, w, crashID)
	p.StopAgent()
	var reconnectAfter time.Time
	if err := p.DB.QueryRow(t.Context(), "SELECT status_updated_at FROM placements WHERE resource_id = $1", crashID).
		Scan(&reconnectAfter); err != nil {
		t.Fatal(err)
	}
	if err := p.StartAgent(t.Context(), p.APIURL, ""); err != nil {
		t.Fatal(err)
	}
	platform.Eventually(
		t,
		2*time.Minute,
		"the reconnected agent to report the crashing placement",
		func(ctx context.Context) error {
			var reported bool
			const query = "SELECT status_updated_at > $2 FROM placements WHERE resource_id = $1"
			if err := p.DB.QueryRow(ctx, query, crashID, reconnectAfter).
				Scan(&reported); err != nil {
				return err
			}
			if !reported {
				return fmt.Errorf("no report since %s", reconnectAfter)
			}
			return nil
		},
	)
	if after := eventIDs(t, w, crashID); !slices.Equal(after, before) {
		t.Errorf("the replay changed the resource's degradation events from %v to %v", before, after)
	}
	if events := crashEvents(t.Context(), t, w, crashID); len(events) != 1 {
		t.Errorf("%d crash-loop events after the replay, want 1", len(events))
	}
}
