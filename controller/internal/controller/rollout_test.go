package controller

import (
	"context"
	"testing"
	"time"

	locolabels "github.com/team-loco/loco/k8sapi/labels"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
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
		Reason: reasonOOMKilled, FinishedAt: metav1.NewTime(now),
	}
	got := replicaProblem(observed(now, crash, *oom), testRestartPolicy, testHealthyAfter, now)
	if got == nil || got.Reason != locov1alpha1.ProblemOutOfMemory {
		t.Fatalf("problem = %+v", got)
	}
}
func observed(now time.Time, pods ...corev1.Pod) []observedReplica {
	replicas := make([]observedReplica, 0, len(pods))
	for _, pod := range pods {
		status := replicaState(&pod, nil, now)
		status.Name = pod.Name
		trackRestarts(&status, nil, time.Time{}, now)
		replicas = append(replicas, observedReplica{pod: pod, status: status})
	}
	return replicas
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
		UID: "deleted-uid", Name: "deleted-name", Namespace: testEnvironmentNamespace(app),
		Labels:            map[string]string{labelApp: testAppName(app)},
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
	kube := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(ownedAtRevision(app, pod, "1")).Build()
	r := &ApplicationReconciler{Client: kube, APIReader: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	r.recordDeletedPod(types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, pod, now)
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(2)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1))
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	got := app.Status.Rollout.Replicas
	if len(got) != 1 || got[0].State != locov1alpha1.ReplicaStopped || got[0].LastExit == nil ||
		got[0].LastExit.ExitCode != 143 {
		t.Fatalf("deleted replicas=%+v", got)
	}
	pod.Status.ContainerStatuses[0].State.Terminated.ExitCode = 137
	pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt = metav1.NewTime(now)
	r.recordDeletedPod(client.ObjectKeyFromObject(app), pod, now)
	next, err := r.observeReplicas(t.Context(), app, dep, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Problem; got == nil || got.Reason != locov1alpha1.ProblemUncleanShutdown {
		t.Fatalf("killed problem=%+v", got)
	}
	if !next.at.Equal(now.Add(deletedPodRetention)) {
		t.Fatalf("killed replica expiry = %s", next.at)
	}
	if _, err := r.observeReplicas(t.Context(), app, dep, next.at); err != nil {
		t.Fatal(err)
	}
	if len(app.Status.Rollout.Replicas) != 0 {
		t.Fatalf("expired replica remains: %+v", app.Status.Rollout.Replicas)
	}
	if app.Status.Rollout.Problem != nil {
		t.Fatalf("expired shutdown problem remains: %+v", app.Status.Rollout.Problem)
	}
	pod.DeletionTimestamp = nil
	pod.Status.ContainerStatuses = nil
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	r.recordDeletedPod(client.ObjectKeyFromObject(app), pod, now)
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	setReadyReplicas(app)
	if app.Status.Rollout.Replicas[0].Ready || app.Status.ReadyReplicas != 0 {
		t.Fatalf("force-deleted Pod still reported ready: %+v", app.Status)
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
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if got := app.Status.Rollout.Replicas[0].State; got != locov1alpha1.ReplicaServing {
		t.Fatalf("listed state=%s", got)
	}
}

func TestReplicaProblemDetail(t *testing.T) {
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
		{"between restarts", corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 2,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: reasonCrashLoopBackOff, Message: "back-off retry",
			}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 1, FinishedAt: metav1.NewTime(now.Add(-time.Second)),
			}},
		}}}}, "exit 1 · 1 of 2 restarts"},
		{"exited before its first restart", corev1.Pod{Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 3},
			}}},
		}}, "exit 3 · 0 of 2 restarts"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := replicaProblem(observed(now, tt.pod), testRestartPolicy, testHealthyAfter, now)
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
			UID: "crash-uid", Name: "crash-replica", Namespace: testEnvironmentNamespace(app),
			Labels: map[string]string{labelApp: testAppName(app)},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff},
			}}}},
		}
		objects := []client.Object{pod}
		if tt.revision != "" {
			objects = append(objects, ownedAtRevision(app, pod, tt.revision))
		}
		kube := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).Build()
		r := &ApplicationReconciler{
			Client: kube, APIReader: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
		}
		dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
		if _, err := r.observeReplicas(context.Background(), app, dep, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := app.Status.Rollout.Problem != nil; got != tt.want {
			t.Errorf("revision %q problem = %+v", tt.revision, app.Status.Rollout.Problem)
		}
	}
}

func TestDeletedReplicaKeepsItsPreviousExit(t *testing.T) {
	now := time.Now()
	app := workspaceApp("deleted-previous-exit", "deleted-previous-exit")
	app.Annotations = map[string]string{locov1alpha1.KeyPlacementRevision: "1"}
	exited := metav1.NewTime(now.Add(-time.Minute))
	pod := &corev1.Pod{
		UID: "previous-exit-uid", Name: "previous-exit", Namespace: testEnvironmentNamespace(app),
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 1,
			State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: exitReasonError, ExitCode: 1, FinishedAt: exited,
			}},
		}}},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(ownedAtRevision(app, pod, "1")).Build()
	r := &ApplicationReconciler{Client: kube, APIReader: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
	r.recordDeletedPod(client.ObjectKeyFromObject(app), pod, now)
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithGeneration(1).
		WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1)).
		WithStatus(appsv1ac.DeploymentStatus().WithObservedGeneration(1))
	if _, err := r.observeReplicas(context.Background(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	got := app.Status.Rollout.Replicas
	if len(got) != 1 || got[0].State != locov1alpha1.ReplicaStopped {
		t.Fatalf("deleted replicas = %+v, want one stopped", got)
	}
	if exit := got[0].LastExit; exit == nil || exit.Reason != exitReasonError || exit.ExitCode != 1 {
		t.Fatalf("deleted replica last exit = %+v, want its previous run's exit", exit)
	}
	if got[0].Detail != "" {
		t.Fatalf("deleted replica detail = %q, want none beside its last exit", got[0].Detail)
	}
}

// ownedAtRevision puts the pod under a ReplicaSet recorded at the revision, as the controller records
// the ReplicaSet that runs a revision's template, and returns that ReplicaSet.
func ownedAtRevision(app *locov1alpha1.Application, pod *corev1.Pod, revision string) *appsv1.ReplicaSet {
	labels := map[string]string{labelApp: testAppName(app)}
	set := &appsv1.ReplicaSet{
		Name: testAppName(app) + "-" + revision, Namespace: testEnvironmentNamespace(app),
		UID: types.UID("rs-" + revision), Labels: labels,
		Annotations: map[string]string{locov1alpha1.KeyPlacementRevision: revision},
		Spec:        appsv1.ReplicaSetSpec{Template: corev1.PodTemplateSpec{Labels: labels}},
	}
	pod.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(set, appsv1.SchemeGroupVersion.WithKind("ReplicaSet")),
	}
	return set
}

func TestMissingApplicationDropsOnlyItsReplicaState(t *testing.T) {
	r := &ApplicationReconciler{APIReader: fake.NewClientBuilder().WithScheme(workspaceTestScheme(t)).Build()}
	key := types.NamespacedName{Namespace: "control", Name: "removed"}
	other := types.NamespacedName{Namespace: key.Namespace, Name: "remaining"}
	now := time.Now()
	for _, owner := range []types.NamespacedName{key, other} {
		r.recordDeletedPod(owner, &corev1.Pod{UID: "deleted-owner-pod"}, now)
	}
	r.podEvents = map[types.NamespacedName][]podEvent{
		key: {{uid: "removed-pod"}}, other: {{uid: "remaining-pod"}},
	}
	if _, err := r.reconcile(t.Context(), reconcile.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if len(r.podEvents[key]) != 0 || len(r.deletedPods[key]) != 0 {
		t.Fatal("removed application's state retained")
	}
	if len(r.podEvents[other]) != 1 || len(r.deletedReplicas(other, now)) != 1 {
		t.Fatal("other application's state removed")
	}
}
