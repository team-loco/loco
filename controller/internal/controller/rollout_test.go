package controller

import (
	"context"
	"testing"
	"time"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRolloutProblemPriority(t *testing.T) {
	now := time.Now()
	crash := corev1.Pod{
		Name: "crash",
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}}},
			},
		},
	}
	oom := crash.DeepCopy()
	oom.Name = "oom"
	oom.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
		Reason: reasonOOMKilled,
	}
	got := replicaProblem([]corev1.Pod{crash, *oom}, time.Minute, now)
	if got == nil || got.Reason != locov1alpha1.ProblemOutOfMemory {
		t.Fatalf("problem = %+v", got)
	}
}
func TestDeletedReplicaExpiry(t *testing.T) {
	r := &ApplicationReconciler{}
	now := time.Now()
	key := types.NamespacedName{Namespace: "rollout-control", Name: "rollout-app"}
	pod := &corev1.Pod{UID: "deleted-pod", Name: "deleted-replica"}
	r.recordDeletedPod(key, pod, now)
	if got := r.deletedReplicas(key, now); len(got) != 1 {
		t.Fatalf("deleted = %v", got)
	}
	if got := r.deletedReplicas(key, now.Add(deletedPodRetention)); len(got) != 0 {
		t.Fatalf("expired = %v", got)
	}
}

func TestPodDeleteHandler(t *testing.T) {
	r := &ApplicationReconciler{}
	key := types.NamespacedName{Namespace: "rollout-control", Name: "rollout-app"}
	pod := &corev1.Pod{
		UID: "deleted-pod",
		Annotations: map[string]string{
			locolabels.AnnotationApplicationNamespace: key.Namespace,
			locolabels.AnnotationApplicationName:      key.Name,
		},
	}
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer q.ShutDown()
	h := r.podHandler(handler.EnqueueRequestsFromMapFunc(applicationForObject))
	h.Delete(context.Background(), event.DeleteEvent{Object: pod, DeleteStateUnknown: true}, q)
	if got := r.deletedReplicas(key, time.Now()); len(got) != 0 {
		t.Fatalf("unknown delete recorded: %v", got)
	}
	h.Delete(context.Background(), event.DeleteEvent{Object: pod}, q)
	if got := r.deletedReplicas(key, time.Now()); len(got) != 1 {
		t.Fatalf("delete not recorded: %v", got)
	}
	if q.Len() != 1 {
		t.Fatalf("queue length = %d", q.Len())
	}
}

func TestObserveDeletedReplica(t *testing.T) {
	now := time.Now()
	app := workspaceApp("deleted-observe", "deleted-observe")
	app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
	app.Status.ObservedPlacementRevision = 2
	pod := &corev1.Pod{
		UID: "deleted-uid", Name: "deleted-name", Namespace: testWorkspaceNamespace(app),
		Labels:            map[string]string{labelApp: testAppName(app)},
		Annotations:       map[string]string{locov1alpha1.KeyPlacementRevision: "1"},
		DeletionTimestamp: new(metav1.NewTime(now)),
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   143,
						FinishedAt: metav1.NewTime(now.Add(-time.Second)),
					},
				},
			},
		}},
	}
	r := &ApplicationReconciler{
		Client:                   fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(),
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	r.recordDeletedPod(types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, pod, now)
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(2)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1))
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	got := app.Status.Rollout.Replicas
	if len(got) != 1 || got[0].State != locov1alpha1.ReplicaStopped || got[0].ExitCode == nil ||
		*got[0].ExitCode != 143 {
		t.Fatalf("deleted replicas=%+v", got)
	}
	pod.Status.ContainerStatuses[0].State.Terminated.ExitCode = 137
	pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt = metav1.NewTime(now)
	r.recordDeletedPod(client.ObjectKeyFromObject(app), pod, now)
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Problem; got == nil || got.Reason != locov1alpha1.ProblemUncleanShutdown {
		t.Fatalf("killed problem=%+v", got)
	}
	dep.Status.WithConditions(
		appsv1ac.DeploymentCondition().
			WithType("Progressing").
			WithStatus("False").
			WithReason(reasonProgressDeadlineExceeded),
	)
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Problem; got == nil || got.Reason != locov1alpha1.ProblemRolloutTimeout {
		t.Fatalf("timeout priority=%+v", got)
	}
	listed := pod.DeepCopy()
	listed.DeletionTimestamp = nil
	listed.Status.ContainerStatuses = nil
	listed.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}
	if err := r.Create(context.Background(), listed); err != nil {
		t.Fatal(err)
	}
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Replicas[0].State; got != locov1alpha1.ReplicaServing {
		t.Fatalf("listed state=%s", got)
	}
	dep.Status.WithConditions(
		appsv1ac.DeploymentCondition().
			WithType("Progressing").
			WithStatus("False").
			WithReason(reasonProgressDeadlineExceeded),
	)
	if err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Problem; got == nil || got.Reason != locov1alpha1.ProblemRolloutTimeout {
		t.Fatalf("timeout=%+v", got)
	}
}

func TestRuntimeProblemDetail(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{"scheduler", corev1.Pod{Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
			Message: "insufficient cpu",
		}}}}, "insufficient cpu"},
		{"memory exhaustion", corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 3,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: reasonOOMKilled, ExitCode: 137,
			}},
		}}}}, "OOMKilled · exit 137 · 3 restarts"},
		{"waiting crash", corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 4,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: reasonCrashLoopBackOff, Message: "back-off retry",
			}},
		}}}}, "CrashLoopBackOff: back-off retry · 4 restarts"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := replicaProblem([]corev1.Pod{tt.pod}, time.Minute, now)
			if got == nil || got.Detail != tt.want {
				t.Fatalf("detail = %+v", got)
			}
		})
	}
}

func TestProblemIgnoresEarlierRevisions(t *testing.T) {
	for _, tt := range []struct {
		revision string
		want     bool
	}{
		{"2", true},
		{"1", false},
		{"", false},
	} {
		app := workspaceApp("problem-revision", "problem-revision")
		app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "2"}
		pod := &corev1.Pod{
			UID: "crash-uid", Name: "crash-replica", Namespace: testWorkspaceNamespace(app),
			Labels:      map[string]string{labelApp: testAppName(app)},
			Annotations: map[string]string{locov1alpha1.KeyPlacementRevision: tt.revision},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff},
			}}}},
		}
		r := &ApplicationReconciler{
			Client:                   fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pod).Build(),
			WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		}
		dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
		if err := r.observeReplicas(context.Background(), app, dep, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := app.Status.Rollout.Problem != nil; got != tt.want {
			t.Errorf("revision %q problem = %+v", tt.revision, app.Status.Rollout.Problem)
		}
	}
}
