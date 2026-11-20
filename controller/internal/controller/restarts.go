package controller

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	reasonKilling = "Killing"
	// eventSlack absorbs the second granularity of kubelet event and termination times.
	eventSlack = time.Second
)

// lastRestart is the newest exit the kubelet recorded before the current container. An exit that
// finishes within the termination grace period after a liveness kill is a health check restart.
func lastRestart(pod *corev1.Pod, livenessKill metav1.Time) *loco.ReplicaRestart {
	var newest *loco.ReplicaRestart
	for _, status := range pod.Status.ContainerStatuses {
		exit := status.LastTerminationState.Terminated
		if exit == nil || newest != nil && !exit.FinishedAt.After(newest.At.Time) {
			continue
		}
		newest = &loco.ReplicaRestart{
			At:       exit.FinishedAt,
			Cause:    restartCause(pod, exit, livenessKill),
			ExitCode: exit.ExitCode,
		}
	}
	return newest
}

func restartCause(pod *corev1.Pod, exit *corev1.ContainerStateTerminated, livenessKill metav1.Time) loco.RestartCause {
	if exit.Reason == reasonOOMKilled {
		return loco.RestartOutOfMemory
	}
	if livenessKill.IsZero() || exit.FinishedAt.IsZero() {
		return loco.RestartExit
	}
	graceSeconds := ptr.Deref(pod.Spec.TerminationGracePeriodSeconds, corev1.DefaultTerminationGracePeriodSeconds)
	grace := time.Duration(graceSeconds) * time.Second
	finished := exit.FinishedAt.Time
	if !finished.Before(livenessKill.Add(-eventSlack)) && !finished.After(livenessKill.Add(grace+eventSlack)) {
		return loco.RestartHealthCheck
	}
	return loco.RestartExit
}

// trackRestarts carries the replica's restarts from its previous report and keeps those since the
// start of its newest run that lasted the health window, at most one past the largest budget.
// Restarts the kubelet counted since add one restart at the last exit; the others have no known
// time and count only in the replica's total.
func trackRestarts(replica, previous *loco.ReplicaStatus, quietSince, now time.Time) {
	var seen int32
	var restarts []metav1.Time
	if previous != nil {
		seen = previous.Restarts
		restarts = append(restarts, previous.RestartsAt...)
	}
	at := metav1.NewTime(now)
	if replica.LastRestart != nil && !replica.LastRestart.At.IsZero() {
		at = replica.LastRestart.At
	}
	if replica.Restarts > seen {
		restarts = append(restarts, at)
	}
	kept := restarts[:0]
	for _, restart := range restarts {
		if !restart.Time.Before(quietSince) {
			kept = append(kept, restart)
		}
	}
	if limit := loco.MaxRestarts + 1; len(kept) > limit {
		kept = kept[len(kept)-limit:]
	}
	replica.RestartsAt = nil
	if len(kept) > 0 {
		replica.RestartsAt = kept
	}
}

// quietSince is when the pod's newest container run that lasted healthyAfter started: the run
// going on now or the one that ended in its last exit. It is zero without one.
func quietSince(pod *corev1.Pod, healthyAfter time.Duration, now time.Time) time.Time {
	var since time.Time
	for _, status := range pod.Status.ContainerStatuses {
		if running := status.State.Running; running != nil && now.Sub(running.StartedAt.Time) >= healthyAfter &&
			running.StartedAt.After(since) {
			since = running.StartedAt.Time
		}
		exit := status.LastTerminationState.Terminated
		if exit != nil && !exit.StartedAt.IsZero() && exit.FinishedAt.Sub(exit.StartedAt.Time) >= healthyAfter &&
			exit.StartedAt.After(since) {
			since = exit.StartedAt.Time
		}
	}
	return since
}

func restartsExhausted(replica loco.ReplicaStatus, policy loco.RestartPolicySpec) bool {
	restarts := int32(len(replica.RestartsAt))
	switch policy.Mode {
	case loco.RestartOnFailure:
		return restarts > policy.MaxRestarts
	case loco.RestartNever:
		return restarts > 0
	default:
		return false
	}
}

func restartedWithin(replica loco.ReplicaStatus, within time.Duration, now time.Time) bool {
	return replica.LastRestart != nil && now.Sub(replica.LastRestart.At.Time) < within
}

func restartingDetail(restart *loco.ReplicaRestart, restarts int, policy loco.RestartPolicySpec) string {
	if policy.Mode == loco.RestartOnFailure {
		return fmt.Sprintf("%s · %d of %d restarts", restart.After(), restarts, policy.MaxRestarts)
	}
	return fmt.Sprintf("%s · %d restarts", restart.After(), restarts)
}
