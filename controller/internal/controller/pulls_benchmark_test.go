package controller

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The cache client isolates event-state lookup and pruning from API and pod-list costs.
type rolloutEventBenchmarkClient struct {
	client.Client
	namespace corev1.Namespace
	current   corev1.Pod
	listed    corev1.Pod
}

func (c *rolloutEventBenchmarkClient) Get(
	_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption,
) error {
	switch obj := obj.(type) {
	case *corev1.Namespace:
		*obj = c.namespace
	case *corev1.Pod:
		*obj = c.current
	}
	return nil
}

func (c *rolloutEventBenchmarkClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	pods, ok := list.(*corev1.PodList)
	if !ok {
		return fmt.Errorf("unexpected list type %T", list)
	}
	pods.Items = []corev1.Pod{c.listed}
	return nil
}

func BenchmarkListRolloutPods(b *testing.B) {
	for _, applications := range []int{1, 1000, 100000} {
		b.Run(fmt.Sprintf("applications=%d", applications), func(b *testing.B) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			app := workspaceApp("target", "target")
			kube := &rolloutEventBenchmarkClient{namespace: corev1.Namespace{
				Name: testEnvironmentNamespace(app), Labels: environmentNamespaceLabels(app),
			}}
			r := &ApplicationReconciler{Client: kube, WorkspaceNamespacePrefix: testWorkspaceNamespacePrefix}
			for i := range applications {
				owner := workspaceApp(fmt.Sprintf("application-%d", i), "resource")
				if i == 0 {
					owner = app
				}
				kube.current = corev1.Pod{
					UID: types.UID(fmt.Sprintf("pod-%d", i)), Name: "benchmark-pod", Namespace: kube.namespace.Name,
					Annotations: ownerAnnotations(owner)}
				if i == 0 {
					kube.listed = kube.current
				}
				requests := r.applicationForPodEvent(b.Context(), &eventsv1.Event{Namespace: kube.namespace.Name,
					Reason: pullReasonPulling, EventTime: metav1.NewMicroTime(time.Now()),
					Regarding: corev1.ObjectReference{Kind: podKind, Name: kube.current.Name, UID: kube.current.UID}})
				if len(requests) != 1 {
					b.Fatal("event was not recorded")
				}
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			retained := float64(after.HeapAlloc-before.HeapAlloc) / float64(applications)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				pods, events, err := r.listRolloutPods(b.Context(), app)
				if err != nil || len(pods) != 1 || len(events) != 1 {
					b.Fatalf("pods=%d events=%d error=%v", len(pods), len(events), err)
				}
			}
			runtime.KeepAlive(r)
			if applications >= 1000 {
				b.ReportMetric(retained, "retained-B/event")
			}
		})
	}
}

func BenchmarkDeletedReplicas(b *testing.B) {
	for _, applications := range []int{1, 1000, 100000} {
		b.Run(fmt.Sprintf("applications=%d", applications), func(b *testing.B) {
			r := &ApplicationReconciler{}
			now := time.Now()
			key := types.NamespacedName{Namespace: "control", Name: "target"}
			for i := range applications {
				owner := types.NamespacedName{Namespace: key.Namespace, Name: fmt.Sprintf("application-%d", i)}
				if i == 0 {
					owner = key
				}
				r.recordDeletedPod(owner, &corev1.Pod{UID: types.UID(fmt.Sprintf("pod-%d", i))}, now)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if len(r.deletedReplicas(key, now)) != 1 {
					b.Fatal("deleted pod lost")
				}
			}
		})
	}
}
