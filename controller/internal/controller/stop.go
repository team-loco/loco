package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// stoppedRevision is the stop recorded for the Application's current placement revision.
func stoppedRevision(app *loco.Application) *loco.StoppedRevision {
	revision, ok := app.PlacementRevision()
	if !ok || app.Status.Rollout == nil || app.Status.Rollout.Stopped == nil ||
		app.Status.Rollout.Stopped.PlacementRevision != revision {
		return nil
	}
	return app.Status.Rollout.Stopped
}

// exhaustedRevision stops the revision when one of its replicas restarted past the budget.
func exhaustedRevision(
	current []observedReplica,
	revision int64,
	policy loco.RestartPolicySpec,
	now time.Time,
) *loco.StoppedRevision {
	for _, replica := range current {
		status := replica.status
		if replica.pod.DeletionTimestamp != nil || !restartsExhausted(status, policy) {
			continue
		}
		last := loco.ReplicaRestart{At: status.RestartsAt[len(status.RestartsAt)-1], Cause: loco.RestartExit}
		if status.LastRestart != nil {
			last = *status.LastRestart
		}
		return &loco.StoppedRevision{
			PlacementRevision: revision,
			Replica:           status.Name,
			Restarts:          int32(len(status.RestartsAt)),
			LastRestart:       last,
			At:                metav1.NewTime(now),
		}
	}
	return nil
}

// stoppedReplicaSets lists the Deployment's ReplicaSets of the stopped revision and reports
// whether a ReplicaSet of another revision still has replicas to keep serving.
func (r *ApplicationReconciler) stoppedReplicaSets(
	ctx context.Context,
	namespace, name string,
	stopped *loco.StoppedRevision,
) ([]appsv1.ReplicaSet, bool, error) {
	var sets appsv1.ReplicaSetList
	if err := r.APIReader.List(ctx, &sets, client.InNamespace(namespace),
		client.MatchingLabels{labelApp: name}); err != nil {
		return nil, false, fmt.Errorf("list replica sets of %s/%s: %w", namespace, name, err)
	}
	revision := strconv.FormatInt(stopped.PlacementRevision, 10)
	var current []appsv1.ReplicaSet
	previousServes := false
	for _, set := range sets.Items {
		if set.Spec.Template.Annotations[loco.KeyPlacementRevision] == revision {
			current = append(current, set)
		} else if ptr.Deref(set.Spec.Replicas, 0) > 0 {
			previousServes = true
		}
	}
	return current, previousServes, nil
}

// scaleDown empties the stopped revision's ReplicaSets. The paused Deployment then scales its one
// active ReplicaSet, the previous revision's, to the full count and leaves the empty ones alone.
func (r *ApplicationReconciler) scaleDown(ctx context.Context, sets []appsv1.ReplicaSet) error {
	for i := range sets {
		set := &sets[i]
		if ptr.Deref(set.Spec.Replicas, 0) == 0 {
			continue
		}
		patch := client.MergeFrom(set.DeepCopy())
		set.Spec.Replicas = new(int32(0))
		if err := r.Patch(ctx, set, patch); err != nil {
			return fmt.Errorf("scale down replica set %s/%s: %w", set.Namespace, set.Name, err)
		}
	}
	return nil
}
