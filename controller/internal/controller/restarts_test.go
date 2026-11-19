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
	policy := loco.RestartPolicySpec{MaxRestarts: 5, WindowSeconds: 600}
	now := replicaTestNow
	first := metav1.NewTime(now.Add(-2 * time.Minute))
	previous := &loco.ReplicaStatus{Restarts: 1, RestartsAt: []metav1.Time{first}}
	exited := metav1.NewTime(now.Add(-time.Second))
	replica := loco.ReplicaStatus{Restarts: 3, LastRestart: &loco.ReplicaRestart{At: exited}}
	trackRestarts(&replica, previous, policy, now)
	want := []metav1.Time{first, exited, exited}
	if len(replica.RestartsAt) != len(want) {
		t.Fatalf("restarts = %v, want %v", replica.RestartsAt, want)
	}
	for i := range want {
		if !replica.RestartsAt[i].Equal(&want[i]) {
			t.Fatalf("restarts = %v, want %v", replica.RestartsAt, want)
		}
	}
	unchanged := loco.ReplicaStatus{Restarts: 3, LastRestart: replica.LastRestart}
	trackRestarts(&unchanged, &replica, policy, now)
	if len(unchanged.RestartsAt) != 3 {
		t.Fatalf("repeated observation counted again: %v", unchanged.RestartsAt)
	}
}

func TestTrackRestartsForgetsRestartsOutsideTheWindow(t *testing.T) {
	policy := loco.RestartPolicySpec{MaxRestarts: 5, WindowSeconds: 600}
	now := replicaTestNow
	old := metav1.NewTime(now.Add(-10 * time.Minute))
	recent := metav1.NewTime(now.Add(-9 * time.Minute))
	previous := &loco.ReplicaStatus{Restarts: 2, RestartsAt: []metav1.Time{old, recent}}
	replica := loco.ReplicaStatus{Restarts: 2}
	trackRestarts(&replica, previous, policy, now)
	if len(replica.RestartsAt) != 1 || !replica.RestartsAt[0].Equal(&recent) {
		t.Fatalf("restarts = %v, want only %v", replica.RestartsAt, recent)
	}
	trackRestarts(&replica, &replica, policy, now.Add(time.Minute))
	if replica.RestartsAt != nil {
		t.Fatalf("restarts = %v after the window, want none", replica.RestartsAt)
	}
}

func TestTrackRestartsKeepsOneRestartPastTheBudget(t *testing.T) {
	policy := loco.RestartPolicySpec{MaxRestarts: 2, WindowSeconds: 600}
	replica := loco.ReplicaStatus{Restarts: 40}
	trackRestarts(&replica, nil, policy, replicaTestNow)
	if len(replica.RestartsAt) != 3 {
		t.Fatalf("restarts = %d entries, want 3", len(replica.RestartsAt))
	}
}

func TestRestartBudgetAllowsMaxRestartsAndStopsOnTheNext(t *testing.T) {
	policy := loco.RestartPolicySpec{MaxRestarts: 2, WindowSeconds: 600}
	now := replicaTestNow
	for restarts, want := range map[int32]bool{0: false, 1: false, 2: false, 3: true} {
		replica := loco.ReplicaStatus{Restarts: restarts}
		trackRestarts(&replica, nil, policy, now)
		if got := restartsExhausted(replica, policy); got != want {
			t.Errorf("%d restarts exhausted = %v, want %v", restarts, got, want)
		}
	}
	zero := loco.RestartPolicySpec{MaxRestarts: 0, WindowSeconds: 600}
	replica := loco.ReplicaStatus{Restarts: 1}
	trackRestarts(&replica, nil, zero, now)
	if !restartsExhausted(replica, zero) {
		t.Error("a budget of zero restarts allowed one")
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
	if got := loco.RestartWindow(600); got != "10 min" {
		t.Errorf("window = %q", got)
	}
	if got := loco.RestartWindow(90); got != "90 s" {
		t.Errorf("window = %q", got)
	}
}

func TestHealthWaitsForQuietReplicas(t *testing.T) {
	r := &ApplicationReconciler{HealthyAfter: testHealthyAfter}
	now := replicaTestNow
	restartedAt := metav1.NewTime(now.Add(-2 * time.Minute))
	restarted := observedReplica{status: loco.ReplicaStatus{LastRestart: &loco.ReplicaRestart{At: restartedAt}}}
	quiet := observedReplica{status: loco.ReplicaStatus{}}

	rollout := &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{quiet}, now)
	if !rollout.Healthy || rollout.HealthyAt != nil {
		t.Fatalf("rollout without restarts = %+v, want healthy at once", rollout)
	}

	rollout = &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{quiet, restarted}, now)
	wantAt := restartedAt.Add(testHealthyAfter)
	if rollout.Healthy || rollout.HealthyAt == nil || !rollout.HealthyAt.Time.Equal(wantAt) {
		t.Fatalf("rollout after a restart = %+v, want healthy at %s", rollout, wantAt)
	}

	rollout = &loco.RolloutStatus{Available: true}
	r.observeHealth(rollout, []observedReplica{restarted}, wantAt)
	if !rollout.Healthy {
		t.Fatalf("rollout at %s = %+v, want healthy", wantAt, rollout)
	}

	rollout = &loco.RolloutStatus{Available: false}
	r.observeHealth(rollout, []observedReplica{quiet}, now)
	if rollout.Healthy || rollout.HealthyAt != nil {
		t.Fatalf("unavailable rollout = %+v, want not healthy", rollout)
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
	pod := restartedPod(testRestartPolicy.MaxRestarts, corev1.ContainerStateTerminated{ExitCode: 1, FinishedAt: exited})
	pod.UID, pod.Name, pod.Namespace = "budget-uid", "budget-replica", testEnvironmentNamespace(app)
	pod.Labels = map[string]string{labelApp: testAppName(app)}
	pod.Annotations = map[string]string{loco.KeyPlacementRevision: "2"}
	client := fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(pod).Build()
	r := &ApplicationReconciler{
		Client: client, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix, HealthyAfter: testHealthyAfter,
	}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1).
			WithReplicas(1).WithUpdatedReplicas(1).WithAvailableReplicas(1))
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	rollout := app.Status.Rollout
	if rollout.Stopped != nil || rollout.Problem == nil || rollout.Problem.Reason != loco.ProblemRestarting {
		t.Fatalf("within budget = %+v, want restarting and not stopped", rollout)
	}
	if want := "exit 1 · 2 of 2 restarts in 10 min"; rollout.Problem.Detail != want {
		t.Fatalf("restarting detail = %q, want %q", rollout.Problem.Detail, want)
	}
	if !rollout.Available || rollout.Healthy || rollout.HealthyAt == nil {
		t.Fatalf("restarted rollout = %+v, want available but not healthy", rollout)
	}

	pod.Status.ContainerStatuses[0].RestartCount++
	if err := client.Status().Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	rollout = app.Status.Rollout
	stopped := rollout.Stopped
	if stopped == nil || stopped.PlacementRevision != 2 || stopped.Replica != pod.Name || stopped.Restarts != 3 ||
		stopped.LastRestart.ExitCode != 1 {
		t.Fatalf("stopped = %+v, want revision 2 stopped after 3 restarts", stopped)
	}
	if rollout.Problem == nil || rollout.Problem.Reason != loco.ProblemRestartsExhausted ||
		rollout.Problem.Detail != "exit 1 · 3 restarts in 10 min" {
		t.Fatalf("problem = %+v, want restarts_exhausted", rollout.Problem)
	}
	if rollout.Available || rollout.Healthy {
		t.Fatalf("stopped rollout = %+v, want neither available nor healthy", rollout)
	}

	app.Annotations[loco.KeyPlacementRevision] = "3"
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
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
	pod.Annotations[loco.KeyPlacementRevision] = "1"
	r := &ApplicationReconciler{
		Client: fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(
			pod, &corev1.Namespace{Name: pod.Namespace, Labels: environmentNamespaceLabels(app)},
		).Build(),
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		HealthyAfter:             testHealthyAfter,
	}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
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
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Replicas[0].LastRestart; got == nil || got.Cause != loco.RestartHealthCheck {
		t.Fatalf("restart after the kill event = %+v, want a health check", got)
	}
}
