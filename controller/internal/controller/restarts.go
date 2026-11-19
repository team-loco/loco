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

// trackRestarts carries the replica's restarts from its previous report, adds the restarts the kubelet
// counted since, and keeps those within the window, at most one past the budget.
func trackRestarts(replica, previous *loco.ReplicaStatus, policy loco.RestartPolicySpec, now time.Time) {
	var seen int32
	var restarts []metav1.Time
	if previous != nil {
		seen = previous.Restarts
		restarts = append(restarts, previous.RestartsAt...)
	}
	limit := int(policy.MaxRestarts) + 1
	at := metav1.NewTime(now)
	if replica.LastRestart != nil && !replica.LastRestart.At.IsZero() {
		at = replica.LastRestart.At
	}
	for range min(int(replica.Restarts-seen), limit) {
		restarts = append(restarts, at)
	}
	window := time.Duration(policy.WindowSeconds) * time.Second
	kept := restarts[:0]
	for _, restart := range restarts {
		if now.Sub(restart.Time) < window {
			kept = append(kept, restart)
		}
	}
	if len(kept) > limit {
		kept = kept[len(kept)-limit:]
	}
	replica.RestartsAt = nil
	if len(kept) > 0 {
		replica.RestartsAt = kept
	}
}

func restartsExhausted(replica loco.ReplicaStatus, policy loco.RestartPolicySpec) bool {
	return int32(len(replica.RestartsAt)) > policy.MaxRestarts
}

func restartedWithin(replica loco.ReplicaStatus, within time.Duration, now time.Time) bool {
	return replica.LastRestart != nil && now.Sub(replica.LastRestart.At.Time) < within
}

func restartingDetail(restart *loco.ReplicaRestart, restarts int, policy loco.RestartPolicySpec) string {
	return fmt.Sprintf("%s · %d of %d restarts in %s",
		restart.After(), restarts, policy.MaxRestarts, loco.RestartWindow(policy.WindowSeconds))
}

func stoppedDetail(stopped *loco.StoppedRevision, policy loco.RestartPolicySpec) string {
	return fmt.Sprintf("%s · %d restarts in %s",
		stopped.LastRestart.After(), stopped.Restarts, loco.RestartWindow(policy.WindowSeconds))
}
