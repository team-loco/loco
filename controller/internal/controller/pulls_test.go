package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestPullEvents(t *testing.T) {
	app := workspaceApp("pulls", "pulls")
	pod := &corev1.Pod{UID: "pod", Name: "pod", Namespace: testEnvironmentNamespace(app),
		Labels: map[string]string{labelApp: testAppName(app)}, Annotations: ownerAnnotations(app)}
	other := pod.DeepCopy()
	other.Name, other.UID = "second-pod", "second-pod"
	other.Annotations = ownerAnnotations(workspaceApp("second-pod", "second-pod"))
	kube := fake.NewClientBuilder().
		WithScheme(workspaceTestScheme(t)).
		WithObjects(pod, other, &corev1.Namespace{Name: pod.Namespace, Labels: environmentNamespaceLabels(app)}).
		Build()
	r := &ApplicationReconciler{Client: kube, APIReader: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	now := time.Now().Truncate(time.Microsecond)
	e := &eventsv1.Event{Namespace: pod.Namespace, Reason: pullReasonPulling, EventTime: metav1.NewMicroTime(now),
		Regarding: corev1.ObjectReference{Kind: podKind, Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID}}
	if got := r.applicationForPodEvent(
		t.Context(),
		e,
	); len(got) != 1 ||
		got[0].NamespacedName != client.ObjectKeyFromObject(app) {
		t.Fatalf("application requests = %v", got)
	}
	e.EventTime = metav1.NewMicroTime(now.Add(-time.Second))
	e.Reason = pullReasonFailed
	r.applicationForPodEvent(t.Context(), e)
	if got := r.podEvents[client.ObjectKeyFromObject(app)][0]; got.pull != pullReasonPulling {
		t.Fatalf("older replaced newest: %+v", got)
	}
	e.Reason = pullReasonPulled
	e.Note = "Successfully pulled image in 2s. Image size: 42 bytes."
	e.Series = &eventsv1.EventSeries{LastObservedTime: metav1.NewMicroTime(now.Add(time.Second)), Count: 2}
	r.applicationForPodEvent(t.Context(), e)
	if got := r.podEvents[client.ObjectKeyFromObject(app)][0]; got.pull != pullReasonPulled ||
		!got.pulledAt.Time.Equal(e.Series.LastObservedTime.Time) {
		t.Fatalf("series update = %+v", got)
	}
	for _, change := range []func(*eventsv1.Event){
		func(e *eventsv1.Event) { e.Reason = "Scheduled" },
		func(e *eventsv1.Event) { e.Namespace = "outside-workspace" },
		func(e *eventsv1.Event) { e.Regarding.Kind = "Node" },
		func(e *eventsv1.Event) { e.Regarding.UID = "old-incarnation" },
	} {
		ignored := e.DeepCopy()
		change(ignored)
		if got := r.applicationForPodEvent(t.Context(), ignored); len(got) != 0 {
			t.Fatalf("accepted event: %+v", ignored)
		}
	}
	e.Regarding.Name, e.Regarding.UID = other.Name, other.UID
	r.applicationForPodEvent(t.Context(), e)
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if _, err := r.observeReplicas(t.Context(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if len(r.podEvents[client.ObjectKeyFromObject(app)]) != 0 {
		t.Fatal("unlisted pod retained")
	}
	if len(r.podEvents[client.ObjectKeyFromObject(workspaceApp("second-pod", "second-pod"))]) != 1 {
		t.Fatal("other application's event removed")
	}
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer q.ShutDown()
	h := r.podHandler(handler.EnqueueRequestsFromMapFunc(applicationForObject))
	h.Delete(t.Context(), event.DeleteEvent{Object: other, DeleteStateUnknown: true}, q)
	if len(r.podEvents) != 0 {
		t.Fatal("deleted pod retained")
	}
}

func TestPullEventTime(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		event eventsv1.Event
		want  time.Time
	}{
		{eventsv1.Event{EventTime: metav1.NewMicroTime(now)}, now},
		{eventsv1.Event{EventTime: metav1.NewMicroTime(now),
			Series: &eventsv1.EventSeries{LastObservedTime: metav1.NewMicroTime(now.Add(time.Second))}},
			now.Add(time.Second)},
		{eventsv1.Event{DeprecatedLastTimestamp: metav1.NewTime(now)}, now},
		{eventsv1.Event{CreationTimestamp: metav1.NewTime(now)}, now},
	} {
		if got := eventTime(&tt.event); !got.Time.Equal(tt.want) {
			t.Fatalf("time = %v, want %v", got, tt.want)
		}
	}
}

func TestReadinessProbeFailureIsTheProblemDetail(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	started := metav1.NewTime(now.Add(-time.Hour))
	app := workspaceApp("readiness", "readiness")
	app.Spec.ServiceSpec.Deployment.HealthCheck = &loco.HealthCheckSpec{
		Path: "/healthz", Interval: 10, Timeout: 1, FailThreshold: 3,
	}
	app.Annotations = map[string]string{loco.KeyPlacementRevision: "1"}
	pod := &corev1.Pod{UID: "readiness-uid", Name: "readiness-replica", Namespace: testEnvironmentNamespace(app),
		Labels: map[string]string{labelApp: testAppName(app)}, Annotations: ownerAnnotations(app),
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", ReadinessProbe: &corev1.Probe{
			PeriodSeconds: 10, FailureThreshold: 3,
		}}}},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
			ContainerStatuses: []corev1.ContainerStatus{{Name: "web", Started: new(true),
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}}},
		}}
	set := ownedAtRevision(app, pod, "1")
	r := &ApplicationReconciler{
		Client: fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).WithObjects(
			set, pod, &corev1.Namespace{Name: pod.Namespace, Labels: environmentNamespaceLabels(app)},
		).Build(),
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		HealthyAfter:             testHealthyAfter,
	}
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
	// detail returns the rollout problem's detail and the replica's detail.
	detail := func() (string, string) {
		t.Helper()
		if _, err := r.observeReplicas(t.Context(), app, dep, now); err != nil {
			t.Fatal(err)
		}
		problem := app.Status.Rollout.Problem
		if problem == nil || problem.Reason != loco.ProblemHealthCheckFailing {
			t.Fatalf("problem = %+v, want a failing health check", problem)
		}
		return problem.Detail, app.Status.Rollout.Replicas[0].Detail
	}
	unhealthy := func(note string, at time.Time) *eventsv1.Event {
		return &eventsv1.Event{Namespace: pod.Namespace, Reason: reasonUnhealthy, Note: note,
			EventTime: metav1.NewMicroTime(at),
			Regarding: corev1.ObjectReference{Kind: podKind, Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID}}
	}

	timedOut := `Readiness probe failed: Get "http://10.0.0.7:8080/healthz": context deadline exceeded`
	stale := unhealthy(timedOut, started.Add(-time.Minute))
	r.applicationForPodEvent(t.Context(), stale)
	got, replica := detail()
	if got != "Health check GET :8080/healthz failed" || replica != detailWaitingForReadiness {
		t.Fatalf("details with only a failure from before the start = %q, %q", got, replica)
	}
	refused := `Readiness probe failed: Get "http://10.0.0.7:8080/healthz": ` +
		"dial tcp 10.0.0.7:8080: connect: connection refused"
	if got := r.applicationForPodEvent(t.Context(), unhealthy(refused, now.Add(-time.Minute))); len(got) != 1 {
		t.Fatalf("readiness event requests = %v", got)
	}
	liveness := unhealthy("Liveness probe failed: HTTP probe failed with statuscode: 500", now)
	r.applicationForPodEvent(t.Context(), liveness)
	older := unhealthy("Readiness probe failed: HTTP probe failed with statuscode: 503", now.Add(-2*time.Minute))
	r.applicationForPodEvent(t.Context(), older)
	want := "Health check GET :8080/healthz failed: connection refused"
	if got, replica = detail(); got != want || replica != want {
		t.Fatalf("details = %q, %q, want %q", got, replica, want)
	}
}
