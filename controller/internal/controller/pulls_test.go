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
)

func TestPullEvents(t *testing.T) {
	app := workspaceApp("pulls", "pulls")
	pod := &corev1.Pod{UID: "pod", Name: "pod", Namespace: testWorkspaceNamespace(app),
		Labels: map[string]string{labelApp: testAppName(app)}, Annotations: ownerAnnotations(app)}
	other := pod.DeepCopy()
	other.Name, other.UID = "second-pod", "second-pod"
	other.Annotations = ownerAnnotations(workspaceApp("second-pod", "second-pod"))
	r := &ApplicationReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(workspaceTestScheme(t)).
			WithObjects(pod, other).
			Build(),
		WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix,
	}
	now := time.Now().Truncate(time.Microsecond)
	e := &eventsv1.Event{Namespace: pod.Namespace, Reason: pullReasonPulling, EventTime: metav1.NewMicroTime(now),
		Regarding: corev1.ObjectReference{Kind: "Pod", Name: pod.Name, Namespace: pod.Namespace, UID: pod.UID}}
	if got := r.applicationForPullEvent(
		t.Context(),
		e,
	); len(got) != 1 ||
		got[0].NamespacedName != client.ObjectKeyFromObject(app) {
		t.Fatalf("application requests = %v", got)
	}
	e.EventTime = metav1.NewMicroTime(now.Add(-time.Second))
	e.Reason = pullReasonFailed
	r.applicationForPullEvent(t.Context(), e)
	if got := r.pullEvents[pod.UID]; got.reason != pullReasonPulling {
		t.Fatalf("older replaced newest: %+v", got)
	}
	e.Reason = pullReasonPulled
	e.Note = "Successfully pulled image in 2s. Image size: 42 bytes."
	e.Series = &eventsv1.EventSeries{LastObservedTime: metav1.NewMicroTime(now.Add(time.Second)), Count: 2}
	r.applicationForPullEvent(t.Context(), e)
	if got := r.pullEvents[pod.UID]; got.reason != pullReasonPulled ||
		!got.at.Time.Equal(e.Series.LastObservedTime.Time) ||
		got.detail != e.Note {
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
		if got := r.applicationForPullEvent(t.Context(), ignored); len(got) != 0 {
			t.Fatalf("accepted event: %+v", ignored)
		}
	}
	e.Regarding.Name, e.Regarding.UID = other.Name, other.UID
	r.applicationForPullEvent(t.Context(), e)
	dep := appsv1ac.Deployment(testAppName(app), pod.Namespace).WithSpec(appsv1ac.DeploymentSpec().WithReplicas(1))
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.observeReplicas(t.Context(), app, dep, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.pullEvents[pod.UID]; ok {
		t.Fatal("unlisted pod retained")
	}
	if _, ok := r.pullEvents[other.UID]; !ok {
		t.Fatal("other application's event removed")
	}
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
	defer q.ShutDown()
	h := r.podHandler(handler.EnqueueRequestsFromMapFunc(applicationForObject))
	h.Delete(t.Context(), event.DeleteEvent{Object: other, DeleteStateUnknown: true}, q)
	if len(r.pullEvents) != 0 {
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
		if got := pullEventTime(&tt.event); !got.Time.Equal(tt.want) {
			t.Fatalf("time = %v, want %v", got, tt.want)
		}
	}
}

func TestPullCacheSelector(t *testing.T) {
	for obj, options := range CacheOptions("", "", false).ByObject {
		if _, ok := obj.(*eventsv1.Event); !ok {
			continue
		}
		if options.Field == nil || options.Field.String() != "regarding.kind=Pod" {
			t.Fatalf("selector = %v", options.Field)
		}
		return
	}
	t.Fatal("events missing from manager cache")
}
