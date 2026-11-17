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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/team-loco/loco/internal/clip"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	pullReasonPulling = "Pulling"
	pullReasonPulled  = "Pulled"
	pullReasonFailed  = "Failed"
)

type pullEvent struct {
	application types.NamespacedName
	reason      string
	detail      string
	at          metav1.Time
}

func (r *ApplicationReconciler) applicationForPullEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	e, ok := obj.(*eventsv1.Event)
	if !ok {
		return nil
	}
	if !strings.HasPrefix(e.Namespace, r.WorkspaceNamespacePrefix) || e.Regarding.Kind != "Pod" {
		return nil
	}
	switch e.Reason {
	case pullReasonPulling, pullReasonPulled, pullReasonFailed:
	default:
		return nil
	}
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
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
	at := pullEventTime(e)
	if old, ok := r.pullEvents[pod.UID]; ok && !at.After(old.at.Time) {
		return nil
	}
	if r.pullEvents == nil {
		r.pullEvents = make(map[types.UID]pullEvent)
	}
	r.pullEvents[pod.UID] = pullEvent{
		application: requests[0].NamespacedName,
		reason:      e.Reason, detail: clip.Bytes(e.Note, replicaDetailBytes), at: at,
	}
	return requests
}

func pullEventTime(e *eventsv1.Event) metav1.Time {
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
) ([]corev1.Pod, map[types.UID]pullEvent, error) {
	// Serialize the list with event updates so pruning cannot discard a newer observation.
	r.pullMu.Lock()
	defer r.pullMu.Unlock()
	var listed corev1.PodList
	if err := r.List(ctx, &listed, client.InNamespace(r.workspaceNamespace(app)),
		client.MatchingLabels{labelApp: loco.ApplicationName(app.Spec.ResourceID)}); err != nil {
		return nil, nil, fmt.Errorf("list rollout replicas: %w", err)
	}
	events := make(map[types.UID]pullEvent, len(listed.Items))
	for _, pod := range listed.Items {
		if e, ok := r.pullEvents[pod.UID]; ok {
			events[pod.UID] = e
		}
	}
	for uid, e := range r.pullEvents {
		if e.application == client.ObjectKeyFromObject(app) {
			if _, ok := events[uid]; !ok {
				delete(r.pullEvents, uid)
			}
		}
	}
	return listed.Items, events, nil
}
