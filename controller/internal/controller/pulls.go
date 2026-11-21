package controller

import (
	"context"
	"fmt"
	"log/slog"
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
	application       types.NamespacedName
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
	recorded := r.podEvents[pod.UID]
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
		r.podEvents = make(map[types.UID]podEvent)
	}
	recorded.application, recorded.namespace = requests[0].NamespacedName, e.Namespace
	r.podEvents[pod.UID] = recorded
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
	events := make(map[types.UID]podEvent, len(listed.Items))
	for _, pod := range listed.Items {
		if e, ok := r.podEvents[pod.UID]; ok {
			events[pod.UID] = e
		}
	}
	for uid, e := range r.podEvents {
		if e.application == client.ObjectKeyFromObject(app) {
			if _, ok := events[uid]; !ok {
				delete(r.podEvents, uid)
			}
		}
	}
	return listed.Items, events, nil
}
