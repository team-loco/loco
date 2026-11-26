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

	"github.com/team-loco/loco/internal/clip"
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

// startupFailedRevision stops invalid image names and container configurations before the
// deploy deadline. Pull and runtime failures can recover and keep their existing retry policy.
func startupFailedRevision(current []observedReplica, revision int64, now time.Time) *loco.StoppedRevision {
	for _, replica := range current {
		for _, status := range replica.pod.Status.ContainerStatuses {
			waiting := status.State.Waiting
			if waiting == nil {
				continue
			}
			switch waiting.Reason {
			case reasonCreateContainerConfigError, reasonInvalidImageName:
				reason := waitingProblemReason(waiting.Reason)
				return &loco.StoppedRevision{
					PlacementRevision: revision,
					Reason:            reason,
					Replica:           replica.status.Name,
					Detail:            clip.Bytes(fmt.Sprintf("%s: %s", waiting.Reason, waiting.Message), 1024),
					At:                metav1.NewTime(now),
				}
			}
		}
	}
	return nil
}

// exhaustedRevision is the stop of the revision when one of its replicas restarted past its restart policy.
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
		restarts := int32(len(status.RestartsAt))
		return &loco.StoppedRevision{
			PlacementRevision: revision,
			Reason:            loco.ProblemRestartsExhausted,
			Replica:           status.Name,
			Restarts:          restarts,
			LastRestart:       &last,
			Detail:            fmt.Sprintf("%s · %d restarts", last.After(), restarts),
			At:                metav1.NewTime(now),
		}
	}
	return nil
}

// timedOutRevision stops the revision when it is still not healthy at its deploy deadline. The
// detail carries the problem that held it back.
func timedOutRevision(
	revision int64,
	timeoutSeconds int32,
	problem *loco.Problem,
	now time.Time,
) *loco.StoppedRevision {
	detail := "not healthy within " + deployTimeout(timeoutSeconds)
	if problem != nil && problem.Detail != "" {
		detail += " · " + problem.Detail
	}
	return &loco.StoppedRevision{
		PlacementRevision: revision,
		Reason:            loco.ProblemDeployTimeout,
		Detail:            clip.Bytes(detail, 1024),
		At:                metav1.NewTime(now),
	}
}

// deployTimeout names a deploy timeout, as in "not healthy within 10 min".
func deployTimeout(seconds int32) string {
	if seconds%60 == 0 {
		return fmt.Sprintf("%d min", seconds/60)
	}
	return fmt.Sprintf("%d s", seconds)
}

const annotationDeploymentRevision = "deployment.kubernetes.io/revision"

// stoppedReplicaSets lists the Deployment's ReplicaSets of the stopped revision and the one that
// serves in its place: the newest ReplicaSet of another revision that still has replicas, else the
// proven revision's. It is nil when neither exists.
func (r *ApplicationReconciler) stoppedReplicaSets(
	ctx context.Context,
	namespace, name string,
	stopped *loco.StoppedRevision,
	proven int64,
) ([]appsv1.ReplicaSet, *appsv1.ReplicaSet, error) {
	// The revisions this reconcile recorded must be seen, so the read skips the cache.
	sets, err := replicaSets(ctx, r.APIReader, namespace, name)
	if err != nil {
		return nil, nil, err
	}
	var current []appsv1.ReplicaSet
	var serving, provenSet *appsv1.ReplicaSet
	for i := range sets {
		set := &sets[i]
		switch setRevision := replicaSetRevision(set); {
		case setRevision == stopped.PlacementRevision:
			current = append(current, *set)
		case ptr.Deref(set.Spec.Replicas, 0) > 0 &&
			(serving == nil || deploymentRevision(set) > deploymentRevision(serving)):
			serving = set
		case proven != 0 && setRevision == proven:
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
