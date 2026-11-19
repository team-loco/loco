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
		if !restartsExhausted(status, policy) {
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

const annotationDeploymentRevision = "deployment.kubernetes.io/revision"

// stoppedReplicaSets lists the Deployment's ReplicaSets of the stopped revision and the one that
// serves in its place: the newest ReplicaSet of another revision that still has replicas, else the
// proven revision's. It is nil when neither exists, so a revision that never proved itself is
// never brought back.
func (r *ApplicationReconciler) stoppedReplicaSets(
	ctx context.Context,
	namespace, name string,
	stopped *loco.StoppedRevision,
	proven int64,
) ([]appsv1.ReplicaSet, *appsv1.ReplicaSet, error) {
	var sets appsv1.ReplicaSetList
	if err := r.APIReader.List(ctx, &sets, client.InNamespace(namespace),
		client.MatchingLabels{labelApp: name}); err != nil {
		return nil, nil, fmt.Errorf("list replica sets of %s/%s: %w", namespace, name, err)
	}
	revision := strconv.FormatInt(stopped.PlacementRevision, 10)
	var current []appsv1.ReplicaSet
	var serving, provenSet *appsv1.ReplicaSet
	for i := range sets.Items {
		set := &sets.Items[i]
		switch setRevision := set.Spec.Template.Annotations[loco.KeyPlacementRevision]; {
		case setRevision == revision:
			current = append(current, *set)
		case ptr.Deref(set.Spec.Replicas, 0) > 0 &&
			(serving == nil || deploymentRevision(set) > deploymentRevision(serving)):
			serving = set
		case proven != 0 && setRevision == strconv.FormatInt(proven, 10):
			provenSet = set
		}
	}
	if serving != nil {
		return current, serving, nil
	}
	return current, provenSet, nil
}

func deploymentRevision(set *appsv1.ReplicaSet) int64 {
	revision, err := strconv.ParseInt(set.Annotations[annotationDeploymentRevision], 10, 64)
	if err != nil {
		return 0
	}
	return revision
}

// scale sets each ReplicaSet's desired replicas. With the stopped revision's ReplicaSets empty and
// the previous one scaled up, the paused Deployment has one active ReplicaSet, keeps it at the
// full count and leaves the empty ones alone.
func (r *ApplicationReconciler) scale(ctx context.Context, sets []appsv1.ReplicaSet, replicas int32) error {
	for i := range sets {
		set := &sets[i]
		if ptr.Deref(set.Spec.Replicas, 0) == replicas {
			continue
		}
		patch := client.MergeFrom(set.DeepCopy())
		set.Spec.Replicas = new(replicas)
		if err := r.Patch(ctx, set, patch); err != nil {
			return fmt.Errorf("scale replica set %s/%s: %w", set.Namespace, set.Name, err)
		}
	}
	return nil
}
