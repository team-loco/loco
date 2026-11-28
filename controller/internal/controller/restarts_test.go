package controller

import (
	"context"
	"testing"
	"time"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func restartedPod(restarts int32, exit corev1.ContainerStateTerminated) *corev1.Pod {
	return &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		RestartCount:         restarts,
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{Terminated: &exit},
	}}}}
}

func TestTrackRestartsCountsTheKubeletDeltaAcrossObservations(t *testing.T) {
	now := replicaTestNow
	first := metav1.NewTime(now.Add(-2 * time.Minute))
	previous := &loco.ReplicaStatus{Restarts: 1, RestartsAt: []metav1.Time{first}}
	exited := metav1.NewTime(now.Add(-time.Second))
	replica := loco.ReplicaStatus{Restarts: 3, LastRestart: &loco.ReplicaRestart{At: exited}}
	trackRestarts(&replica, previous, time.Time{}, now)
	want := []metav1.Time{first, exited}
	if len(replica.RestartsAt) != len(want) {
		t.Fatalf("restarts = %v, want %v", replica.RestartsAt, want)
	}
	for i := range want {
		if !replica.RestartsAt[i].Equal(&want[i]) {
			t.Fatalf("restarts = %v, want %v", replica.RestartsAt, want)
		}
	}
	unchanged := loco.ReplicaStatus{Restarts: 3, LastRestart: replica.LastRestart}
	trackRestarts(&unchanged, &replica, time.Time{}, now)
	if len(unchanged.RestartsAt) != 2 {
		t.Fatalf("repeated observation counted again: %v", unchanged.RestartsAt)
	}
}

func TestTrackRestartsForgetsRestartsBeforeAQuietRun(t *testing.T) {
	now := replicaTestNow
	old := metav1.NewTime(now.Add(-time.Hour))
	recent := metav1.NewTime(now.Add(-time.Minute))
	previous := &loco.ReplicaStatus{Restarts: 2, RestartsAt: []metav1.Time{old, recent}}
	replica := loco.ReplicaStatus{Restarts: 2}
	trackRestarts(&replica, previous, now.Add(-30*time.Minute), now)
	if len(replica.RestartsAt) != 1 || !replica.RestartsAt[0].Equal(&recent) {
		t.Fatalf("restarts = %v, want only %v", replica.RestartsAt, recent)
	}
	trackRestarts(&replica, &replica, recent.Add(time.Second), now)
	if replica.RestartsAt != nil {
		t.Fatalf("restarts = %v after a quiet run, want none", replica.RestartsAt)
	}
}

func TestRestartsMissedBetweenObservationsCountOnce(t *testing.T) {
	policy := loco.RestartPolicySpec{Mode: loco.RestartOnFailure, MaxRestarts: 2}
	exited := metav1.NewTime(replicaTestNow.Add(-time.Minute))
	replica := loco.ReplicaStatus{Restarts: 40, LastRestart: &loco.ReplicaRestart{At: exited}}
	trackRestarts(&replica, &loco.ReplicaStatus{Restarts: 34}, time.Time{}, replicaTestNow)
	if len(replica.RestartsAt) != 1 || !replica.RestartsAt[0].Equal(&exited) {
		t.Fatalf("restarts = %v, want only the last exit", replica.RestartsAt)
	}
	if restartsExhausted(replica, policy) {
		t.Fatal("restarts without a known time used up the budget")
	}
}

func TestTrackRestartsKeepsOneRestartPastTheLargestBudget(t *testing.T) {
	replica := restartTimes(40)
	if len(replica.RestartsAt) != loco.MaxRestarts+1 {
		t.Fatalf("restarts = %d entries, want %d", len(replica.RestartsAt), loco.MaxRestarts+1)
	}
}

// restartTimes observes a replica restarting once a second.
func restartTimes(restarts int32) loco.ReplicaStatus {
	replica := loco.ReplicaStatus{}
	for restart := range restarts {
		next := loco.ReplicaStatus{Restarts: restart + 1}
		trackRestarts(&next, &replica, time.Time{}, replicaTestNow.Add(time.Duration(restart)*time.Second))
		replica = next
	}
	return replica
}

func TestRestartPolicyStopsTheRevision(t *testing.T) {
	onFailure := loco.RestartPolicySpec{Mode: loco.RestartOnFailure, MaxRestarts: 2}
	never := loco.RestartPolicySpec{Mode: loco.RestartNever}
	always := loco.RestartPolicySpec{Mode: loco.RestartAlways}
	for _, tt := range []struct {
		policy   loco.RestartPolicySpec
		restarts int32
		want     bool
	}{
		{onFailure, 0, false}, {onFailure, 2, false}, {onFailure, 3, true},
		{never, 0, false}, {never, 1, true},
		{always, 0, false}, {always, 40, false},
	} {
		if got := restartsExhausted(restartTimes(tt.restarts), tt.policy); got != tt.want {
			t.Errorf("%s after %d restarts exhausted = %v, want %v", tt.policy.Mode, tt.restarts, got, tt.want)
		}
	}
}

func TestQuietSinceIsTheStartOfTheNewestRunThatLastedTheHealthWindow(t *testing.T) {
	now := replicaTestNow
	at := func(ago time.Duration) metav1.Time { return metav1.NewTime(now.Add(-ago)) }
	pod := func(running metav1.Time, exit corev1.ContainerStateTerminated) *corev1.Pod {
		return &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: running}},
			LastTerminationState: corev1.ContainerState{Terminated: &exit},
		}}}}
	}
	short := corev1.ContainerStateTerminated{StartedAt: at(time.Hour), FinishedAt: at(time.Hour - time.Second)}
	long := corev1.ContainerStateTerminated{StartedAt: at(time.Hour), FinishedAt: at(time.Minute)}
	for _, tt := range []struct {
		name string
		pod  *corev1.Pod
		want time.Time
	}{
		{"short runs", pod(at(time.Minute), short), time.Time{}},
		{"running for the window", pod(at(testHealthyAfter), short), at(testHealthyAfter).Time},
		{"exited after the window", pod(at(time.Second), long), long.StartedAt.Time},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := quietSince(tt.pod, testHealthyAfter, now); !got.Equal(tt.want) {
				t.Fatalf("quiet since = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLastRestartCause(t *testing.T) {
	now := replicaTestNow
	finished := metav1.NewTime(now)
	grace := int64(35)
	for _, tt := range []struct {
		name string
		exit corev1.ContainerStateTerminated
		kill metav1.Time
		want loco.RestartCause
	}{
		{"exit", corev1.ContainerStateTerminated{ExitCode: 1, FinishedAt: finished}, metav1.Time{}, loco.RestartExit},
		{"memory", corev1.ContainerStateTerminated{
			Reason: reasonOOMKilled, ExitCode: 137, FinishedAt: finished,
		}, metav1.NewTime(now), loco.RestartOutOfMemory},
		{"liveness kill", corev1.ContainerStateTerminated{ExitCode: 137, FinishedAt: finished},
			metav1.NewTime(now.Add(-30 * time.Second)), loco.RestartHealthCheck},
		{"graceful exit after a liveness kill", corev1.ContainerStateTerminated{ExitCode: 0, FinishedAt: finished},
			metav1.NewTime(now.Add(-2 * time.Second)), loco.RestartHealthCheck},
		{"kill of an earlier run", corev1.ContainerStateTerminated{ExitCode: 137, FinishedAt: finished},
			metav1.NewTime(now.Add(-time.Duration(grace+5) * time.Second)), loco.RestartExit},
		{"kill after the exit", corev1.ContainerStateTerminated{ExitCode: 1, FinishedAt: finished},
			metav1.NewTime(now.Add(10 * time.Second)), loco.RestartExit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := restartedPod(1, tt.exit)
			pod.Spec.TerminationGracePeriodSeconds = &grace
			got := lastRestart(pod, tt.kill)
			if got == nil || got.Cause != tt.want || got.ExitCode != tt.exit.ExitCode || !got.At.Equal(&finished) {
				t.Fatalf("last restart = %+v, want cause %s", got, tt.want)
			}
		})
	}
}

func TestRestartAfterText(t *testing.T) {
	for restart, want := range map[loco.ReplicaRestart]string{
		{Cause: loco.RestartExit, ExitCode: 1}:          "exit 1",
		{Cause: loco.RestartHealthCheck, ExitCode: 137}: "failing its health check",
		{Cause: loco.RestartOutOfMemory, ExitCode: 137}: "running out of memory",
	} {
		if got := restart.After(); got != want {
			t.Errorf("%+v after = %q, want %q", restart, got, want)
		}
	}
	if got := deployTimeout(600); got != "10 min" {
		t.Errorf("timeout = %q", got)
	}
	if got := deployTimeout(90); got != "90 s" {
		t.Errorf("timeout = %q", got)
	}
}

func TestHealthWaitsForQuietReplicas(t *testing.T) {
	r := &ApplicationReconciler{HealthyAfter: testHealthyAfter}
	now := replicaTestNow
	restartedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	restarted := observedReplica{status: loco.ReplicaStatus{LastRestart: &loco.ReplicaRestart{At: restartedAt}}}
	quiet := observedReplica{status: loco.ReplicaStatus{}}

	rollout := &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{quiet}, 2, true, now)
	if !rollout.Healthy || rollout.HealthyAt != nil {
		t.Fatalf("rollout without restarts = %+v, want healthy at once", rollout)
	}

	rollout = &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{quiet, restarted}, 2, true, now)
	wantAt := restartedAt.Add(testHealthyAfter)
	if rollout.Healthy || rollout.HealthyAt == nil || !rollout.HealthyAt.Time.Equal(wantAt) {
		t.Fatalf("rollout after a restart = %+v, want healthy at %s", rollout, wantAt)
	}

	rollout = &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{restarted}, 2, true, wantAt)
	if !rollout.Healthy {
		t.Fatalf("rollout at %s = %+v, want healthy", wantAt, rollout)
	}

	rollout = &loco.RolloutStatus{Available: false}
	r.observeHealth(rollout, []observedReplica{quiet}, 2, true, now)
	if rollout.Healthy || rollout.HealthyAt != nil {
		t.Fatalf("unavailable rollout = %+v, want not healthy", rollout)
	}
}

func TestRevisionIsProvenOnceEveryReplicaServedForTheHealthWindow(t *testing.T) {
	r := &ApplicationReconciler{HealthyAfter: testHealthyAfter}
	now := replicaTestNow
	serving := func(since time.Duration) observedReplica {
		at := metav1.NewTime(now.Add(-since))
		return observedReplica{status: loco.ReplicaStatus{State: loco.ReplicaServing, StateSince: &at}}
	}
	for _, tt := range []struct {
		name     string
		replicas []observedReplica
		settled  bool
		want     int64
	}{
		{"every replica served the window", []observedReplica{serving(testHealthyAfter), serving(time.Hour)}, true, 2},
		{"a replica is not yet available", []observedReplica{serving(testHealthyAfter), serving(time.Hour)}, false, 1},
		{"a replica became ready recently", []observedReplica{serving(time.Hour), serving(time.Minute)}, true, 1},
		{"a replica is starting", []observedReplica{serving(time.Hour), {status: loco.ReplicaStatus{
			State: loco.ReplicaStarting, StateSince: new(metav1.NewTime(now.Add(-time.Hour))),
		}}}, true, 1},
		{"no replica", nil, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rollout := &loco.RolloutStatus{Available: true, ProvenRevision: 1}
			r.observeHealth(rollout, tt.replicas, 2, tt.settled, now)
			if rollout.ProvenRevision != tt.want {
				t.Fatalf("proven revision = %d, want %d", rollout.ProvenRevision, tt.want)
			}
		})
	}
}

func TestHistoricalRestartDoesNotOverrideALaterHealthFailure(t *testing.T) {
	now := replicaTestNow
	started := true
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "recovered-service", ReadinessProbe: &corev1.Probe{PeriodSeconds: 10, FailureThreshold: 3},
	}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Name:    "recovered-service",
		Started: &started,
		State: corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(now.Add(-time.Hour))},
		},
	}}}}
	replica := loco.ReplicaStatus{LastRestart: &loco.ReplicaRestart{
		At: metav1.NewTime(now.Add(-time.Hour)), Cause: loco.RestartOutOfMemory,
	}}
	if reason := replicaProblemReason(pod, replica, testHealthyAfter, now); reason != loco.ProblemHealthCheckFailing {
		t.Fatalf("historical restart overrode a health failure: %q", reason)
	}
}

func TestObserveReplicasStopsTheRevisionPastTheBudget(t *testing.T) {
	now := replicaTestNow
	app := workspaceApp("restart-budget", "restart-budget")
	app.Annotations = map[string]string{loco.KeyPlacementRevision: "2"}
	app.Status.ObservedPlacementRevision = 2
	exited := metav1.NewTime(now.Add(-time.Second))
	pod := restartedPod(1, corev1.ContainerStateTerminated{ExitCode: 1, FinishedAt: exited})
	pod.UID, pod.Name, pod.Namespace = "budget-uid", "budget-replica", testEnvironmentNamespace(app)
	pod.Labels = map[string]string{labelApp: testAppName(app)}
	set := ownedAtRevision(app, pod, "2")
	client := fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(pod, set).Build()
	r := &ApplicationReconciler{
		Client: client, APIReader: client, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		HealthyAfter: testHealthyAfter,
	}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1).
			WithReplicas(1).WithUpdatedReplicas(1).WithReadyReplicas(1).WithAvailableReplicas(1))
	restart := func() {
		t.Helper()
		pod.Status.ContainerStatuses[0].RestartCount++
		if err := client.Status().Update(context.Background(), pod); err != nil {
			t.Fatal(err)
		}
		if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	restart()
	rollout := app.Status.Rollout
	if rollout.Stopped != nil || rollout.Problem == nil || rollout.Problem.Reason != loco.ProblemRestarting {
		t.Fatalf("within budget = %+v, want restarting and not stopped", rollout)
	}
	if want := "exit 1 · 2 of 2 restarts"; rollout.Problem.Detail != want {
		t.Fatalf("restarting detail = %q, want %q", rollout.Problem.Detail, want)
	}
	if !rollout.Available || rollout.Healthy || rollout.HealthyAt == nil {
		t.Fatalf("restarted rollout = %+v, want available but not healthy", rollout)
	}

	restart()
	rollout = app.Status.Rollout
	stopped := rollout.Stopped
	if stopped == nil || stopped.PlacementRevision != 2 || stopped.Replica != pod.Name || stopped.Restarts != 3 ||
		stopped.LastRestart.ExitCode != 1 {
		t.Fatalf("stopped = %+v, want revision 2 stopped after 3 restarts", stopped)
	}
	if rollout.Problem == nil || rollout.Problem.Reason != loco.ProblemRestartsExhausted ||
		rollout.Problem.Detail != "exit 1 · 3 restarts" {
		t.Fatalf("problem = %+v, want restarts_exhausted", rollout.Problem)
	}
	if rollout.Available || rollout.Healthy {
		t.Fatalf("stopped rollout = %+v, want neither available nor healthy", rollout)
	}

	app.Annotations[loco.KeyPlacementRevision] = "3"
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Stopped != nil {
		t.Fatalf("a new placement revision kept the stop: %+v", app.Status.Rollout.Stopped)
	}
}

func TestLivenessKillEventMakesTheRestartAHealthCheck(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	app := workspaceApp("liveness", "liveness")
	app.Annotations = map[string]string{loco.KeyPlacementRevision: "1"}
	pod := restartedPod(1, corev1.ContainerStateTerminated{ExitCode: 137, FinishedAt: metav1.NewTime(now)})
	pod.UID, pod.Name, pod.Namespace = "liveness-uid", "liveness-replica", testEnvironmentNamespace(app)
	pod.Labels = map[string]string{labelApp: testAppName(app)}
	pod.Annotations = ownerAnnotations(app)
	kube := fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(
		pod, ownedAtRevision(app, pod, "1"),
		&corev1.Namespace{Name: pod.Namespace, Labels: environmentNamespaceLabels(app)},
	).Build()
	r := &ApplicationReconciler{
		Client:                   kube,
		APIReader:                kube,
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		HealthyAfter:             testHealthyAfter,
	}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Replicas[0].LastRestart; got == nil || got.Cause != loco.RestartExit {
		t.Fatalf("restart before the kill event = %+v, want an exit", got)
	}
	kill := &eventsv1.Event{
		Namespace: pod.Namespace, Reason: reasonKilling, EventTime: metav1.NewMicroTime(now.Add(-30 * time.Second)),
		Note:      "Container app failed liveness probe, will be restarted",
		Regarding: corev1.ObjectReference{Kind: podKind, Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID},
	}
	if got := r.applicationForPodEvent(context.Background(), kill); len(got) != 1 {
		t.Fatalf("kill event requests = %v", got)
	}
	stopping := kill.DeepCopy()
	stopping.Note = "Stopping container app"
	stopping.EventTime = metav1.NewMicroTime(now)
	if got := r.applicationForPodEvent(context.Background(), stopping); len(got) != 0 {
		t.Fatalf("a deletion's Killing event was recorded: %v", got)
	}
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Replicas[0].LastRestart; got == nil || got.Cause != loco.RestartHealthCheck {
		t.Fatalf("restart after the kill event = %+v, want a health check", got)
	}
}
