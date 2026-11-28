package controller

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	podKind           = "Pod"
	pullReasonPulling = "Pulling"
	pullReasonPulled  = "Pulled"
	pullReasonFailed  = "Failed"
)

// kubeletLivenessKill is in the note of the Killing event the kubelet records before it restarts a container.
const kubeletLivenessKill = "failed liveness probe"

const (
	reasonUnhealthy = "Unhealthy"
	// kubeletReadinessFailed opens the note of the Unhealthy event the kubelet records for a failed readiness probe.
	kubeletReadinessFailed = "Readiness probe failed:"
)

// podEvent holds the newest image pull, liveness kill and readiness probe failure the kubelet reported for a pod.
type podEvent struct {
	uid               types.UID
	namespace         string
	pull              string
	pulledAt          metav1.Time
	livenessKill      metav1.Time
	readinessFailure  string
	readinessFailedAt metav1.Time
}

func (r *ApplicationReconciler) applicationForPodEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	e, ok := obj.(*eventsv1.Event)
	if !ok {
		return nil
	}
	if e.Regarding.Kind != podKind {
		return nil
	}
	kill := e.Reason == reasonKilling && strings.Contains(e.Note, kubeletLivenessKill)
	readiness := e.Reason == reasonUnhealthy && strings.HasPrefix(e.Note, kubeletReadinessFailed)
	pull := false
	switch e.Reason {
	case pullReasonPulling, pullReasonPulled, pullReasonFailed:
		pull = true
	default:
		if !kill && !readiness {
			return nil
		}
	}
	r.podEventsMu.Lock()
	defer r.podEventsMu.Unlock()
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, client.ObjectKey{Name: e.Namespace}, ns); err != nil {
		if !apierrors.IsNotFound(err) {
			slog.WarnContext(ctx, "failed to resolve pull event namespace", "namespace", e.Namespace, "error", err)
		}
		return nil
	}
	if !ns.DeletionTimestamp.IsZero() || !workspaceNamespaceSelector().Matches(labels.Set(ns.Labels)) {
		return nil
	}
	pod := &corev1.Pod{}
	key := client.ObjectKey{Namespace: e.Namespace, Name: e.Regarding.Name}
	if err := r.Get(ctx, key, pod); err != nil {
		if !apierrors.IsNotFound(err) {
			slog.WarnContext(ctx, "failed to resolve pull event pod", "pod", key, "error", err)
		}
		return nil
	}
	if pod.UID != e.Regarding.UID {
		return nil
	}
	requests := applicationForObject(ctx, pod)
	if len(requests) == 0 {
		return nil
	}
	at := eventTime(e)
	application := requests[0].NamespacedName
	events := r.podEvents[application]
	index := slices.IndexFunc(events, func(recorded podEvent) bool { return recorded.uid == pod.UID })
	recorded := podEvent{uid: pod.UID, namespace: e.Namespace}
	if index >= 0 {
		recorded = events[index]
	}
	switch {
	case kill && at.After(recorded.livenessKill.Time):
		recorded.livenessKill = at
	case readiness && at.After(recorded.readinessFailedAt.Time):
		recorded.readinessFailure, recorded.readinessFailedAt = e.Note, at
	case pull && at.After(recorded.pulledAt.Time):
		recorded.pull, recorded.pulledAt = e.Reason, at
	default:
		return nil
	}
	if r.podEvents == nil {
		r.podEvents = make(map[types.NamespacedName][]podEvent)
	}
	if index < 0 {
		r.podEvents[application] = append(events, recorded)
	} else {
		events[index] = recorded
	}
	return requests
}

// currentReadinessFailure is the newest readiness probe failure reported since the pod's containers last started.
func (e podEvent) currentReadinessFailure(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if running := status.State.Running; running != nil && e.readinessFailedAt.Before(&running.StartedAt) {
			return ""
		}
	}
	return e.readinessFailure
}

func eventTime(e *eventsv1.Event) metav1.Time {
	at := e.EventTime.Time
	if e.Series != nil && e.Series.LastObservedTime.After(at) {
		at = e.Series.LastObservedTime.Time
	}
	if e.DeprecatedLastTimestamp.After(at) {
		at = e.DeprecatedLastTimestamp.Time
	}
	if at.IsZero() {
		at = e.CreationTimestamp.Time
	}
	return metav1.NewTime(at)
}

func (r *ApplicationReconciler) listRolloutPods(
	ctx context.Context,
	app *loco.Application,
) ([]corev1.Pod, map[types.UID]podEvent, error) {
	// Serialize the list with event updates so pruning cannot discard a newer observation.
	r.podEventsMu.Lock()
	defer r.podEventsMu.Unlock()
	var listed corev1.PodList
	if err := r.List(ctx, &listed, client.InNamespace(r.environmentNamespace(app)),
		client.MatchingLabels{labelApp: loco.WorkloadName(app.Spec.ResourceID)}); err != nil {
		return nil, nil, fmt.Errorf("list rollout replicas: %w", err)
	}
	application := client.ObjectKeyFromObject(app)
	recorded := r.podEvents[application]
	events := make(map[types.UID]podEvent, len(listed.Items))
	listedUIDs := make(map[types.UID]bool, len(listed.Items))
	for _, pod := range listed.Items {
		listedUIDs[pod.UID] = true
	}
	recorded = slices.DeleteFunc(recorded, func(e podEvent) bool {
		if !listedUIDs[e.uid] {
			return true
		}
		events[e.uid] = e
		return false
	})
	if len(recorded) == 0 {
		delete(r.podEvents, application)
	} else {
		r.podEvents[application] = recorded
	}
	return listed.Items, events, nil
}
