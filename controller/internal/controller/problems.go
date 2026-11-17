package controller

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const reasonOOMKilled = "OOMKilled"

func replicaProblemReason(pods []corev1.Pod, recoveryWindow time.Duration, now time.Time) locov1alpha1.ProblemReason {
	found := map[locov1alpha1.ProblemReason]bool{}
	for _, pod := range pods {
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse &&
				c.Reason == corev1.PodReasonUnschedulable {
				found[locov1alpha1.ProblemUnschedulable] = true
			}
		}
		for _, status := range pod.Status.ContainerStatuses {
			running := status.State.Running
			recovering := replicaRecoveringAt(pod, status, recoveryWindow, now)
			waiting := status.State.Waiting
			if waiting != nil {
				if reason := waitingProblemReason(waiting.Reason, waiting.Message); reason != "" {
					found[reason] = true
				}
			}
			backoff := waiting != nil && waiting.Reason == reasonCrashLoopBackOff
			lastExit := status.LastTerminationState.Terminated
			activeExit := status.State.Terminated
			if lastExit != nil && (backoff || recovering) {
				found[locov1alpha1.ProblemCrashLoop] = true
				if lastExit.Reason == reasonOOMKilled {
					found[locov1alpha1.ProblemOutOfMemory] = true
				}
			}
			if activeExit != nil {
				if status.RestartCount > 0 {
					found[locov1alpha1.ProblemCrashLoop] = true
				}
				if activeExit.Reason == reasonOOMKilled {
					found[locov1alpha1.ProblemOutOfMemory] = true
				}
			}
			if status.Ready {
				continue
			}

			if running != nil && ptr.Deref(status.Started, false) {
				for _, container := range pod.Spec.Containers {
					probe := container.ReadinessProbe
					if container.Name != status.Name || probe == nil {
						continue
					}
					// Readiness needs the configured startup grace and failure window.
					grace := time.Duration(probe.InitialDelaySeconds)*time.Second +
						time.Duration(probe.PeriodSeconds)*time.Duration(probe.FailureThreshold)*time.Second
					startedAt := status.State.Running.StartedAt
					if !startedAt.IsZero() && now.Sub(startedAt.Time) >= grace {
						found[locov1alpha1.ProblemHealthCheckFailing] = true
					}
				}
			}
		}
	}
	var best locov1alpha1.ProblemReason
	for reason := range found {
		if best == "" || locov1alpha1.ProblemHigherPriority(reason, best) {
			best = reason
		}
	}
	return best
}

// A retry stays unhealthy until it sustains readiness.
func replicaRecoveringAt(
	pod corev1.Pod,
	status corev1.ContainerStatus,
	recoveryWindow time.Duration,
	now time.Time,
) bool {
	running := status.State.Running
	if running == nil {
		return false
	}
	if running.StartedAt.IsZero() || now.Sub(running.StartedAt.Time) < recoveryWindow {
		return true
	}
	if status.Ready {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.ContainersReady && condition.Status == corev1.ConditionFalse &&
			!condition.LastTransitionTime.IsZero() && !condition.LastTransitionTime.After(running.StartedAt.Time) {
			return true
		}
	}
	return false
}
