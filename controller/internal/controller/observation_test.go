package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestReadinessGraceSchedulesItsDiagnostic(t *testing.T) {
	started := replicaTestNow
	pod := observationPod(started, false)
	pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{InitialDelaySeconds: 1, PeriodSeconds: 2, FailureThreshold: 3}
	r, app, dep := observationFixture(pod)
	dep.Status.WithReadyReplicas(0).WithAvailableReplicas(0)
	next, err := r.observeReplicas(t.Context(), app, dep, started.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Problem != nil {
		t.Fatalf("health failure reported before grace: %+v", app.Status.Rollout.Problem)
	}
	if !next.at.Equal(started.Add(7 * time.Second)) {
		t.Fatalf("next readiness observation = %s", next.at)
	}
	if _, err := r.observeReplicas(t.Context(), app, dep, next.at); err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Problem == nil {
		t.Fatal("unchanged unready Pod has no diagnostic at grace expiry")
	}
	if app.Status.Rollout.Problem.Reason != loco.ProblemHealthCheckFailing {
		t.Fatalf("problem=%+v", app.Status.Rollout.Problem)
	}
}

func TestRestartRecoverySchedulesHealthAndProof(t *testing.T) {
	started := replicaTestNow
	pod := observationPod(started.Add(time.Second), true)
	pod.Status.ContainerStatuses[0].RestartCount = 1
	pod.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
		ExitCode: 1, FinishedAt: metav1.NewTime(started),
	}
	r, app, dep := observationFixture(pod)
	r.HealthyAfter = 10 * time.Second
	next, err := r.observeReplicas(t.Context(), app, dep, started.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Healthy {
		t.Fatal("recently restarted Pod is already healthy")
	}
	if !next.at.Equal(started.Add(10 * time.Second)) {
		t.Fatalf("next recovery observation=%s", next.at)
	}
	next, err = r.observeReplicas(t.Context(), app, dep, next.at)
	if err != nil {
		t.Fatal(err)
	}
	if !app.Status.Rollout.Healthy {
		t.Fatal("unchanged Pod did not recover after the quiet window")
	}
	if app.Status.Rollout.Problem != nil {
		t.Fatalf("recovered rollout retains a problem: %+v", app.Status.Rollout.Problem)
	}
	if _, err := r.observeReplicas(t.Context(), app, dep, next.at); err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.ProvenRevision != 2 {
		t.Fatalf("quiet revision remains unproven: %+v", app.Status.Rollout)
	}
}

func TestTerminationSchedulesDrainCompletion(t *testing.T) {
	started := replicaTestNow
	pod := observationPod(started, false)
	pod.Finalizers = []string{"test"}
	pod.DeletionTimestamp = new(metav1.NewTime(started.Add(5 * time.Second)))
	pod.DeletionGracePeriodSeconds = new(int64(5))
	pod.Spec.Containers[0].Lifecycle = &corev1.Lifecycle{
		PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 2}},
	}
	r, app, dep := observationFixture(pod)
	next, err := r.observeReplicas(t.Context(), app, dep, started.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Replicas[0].Detail != "draining" {
		t.Fatalf("replica=%+v", app.Status.Rollout.Replicas[0])
	}
	if !next.at.Equal(started.Add(2 * time.Second)) {
		t.Fatalf("next drain observation=%s", next.at)
	}
	next, err = r.observeReplicas(t.Context(), app, dep, next.at)
	if err != nil {
		t.Fatal(err)
	}
	if app.Status.Rollout.Replicas[0].Detail != "SIGTERM sent" {
		t.Fatalf("unchanged Pod did not finish draining: %+v", app.Status.Rollout.Replicas[0])
	}
	if !next.at.Equal(started.Add(5 * time.Second)) {
		t.Fatalf("next grace observation=%s", next.at)
	}
}

func observationPod(started time.Time, ready bool) *corev1.Pod {
	condition := corev1.ConditionFalse
	if ready {
		condition = corev1.ConditionTrue
	}
	return &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "observation-container"}}},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			{Type: corev1.PodReady, Status: condition, LastTransitionTime: metav1.NewTime(started)},
		}, ContainerStatuses: []corev1.ContainerStatus{{Name: "observation-container", Ready: ready, Started: new(true),
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(started)}}}}},
	}
}

func observationFixture(
	pod *corev1.Pod,
) (*ApplicationReconciler, *loco.Application, *appsv1ac.DeploymentApplyConfiguration) {
	app := workspaceApp("timed-observation", "timed-observation")
	app.Annotations = map[string]string{loco.KeyPlacementRevision: "2"}
	app.Status.ObservedPlacementRevision = 2
	pod.Name, pod.UID, pod.Namespace = "timed-pod", "timed-pod", testEnvironmentNamespace(app)
	pod.Labels = map[string]string{labelApp: testAppName(app)}
	set := ownedAtRevision(app, pod, "2")
	kube := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod, set).Build()
	r := &ApplicationReconciler{Client: kube, APIReader: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1).WithReplicas(1).
			WithUpdatedReplicas(1).WithReadyReplicas(1).WithAvailableReplicas(1))
	return r, app, dep
}
