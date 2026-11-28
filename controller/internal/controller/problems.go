package controller

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	reasonOOMKilled                  = "OOMKilled"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonErrImagePull               = "ErrImagePull"
	reasonInvalidImageName           = "InvalidImageName"
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
)

func replicaProblemReason(
	pod corev1.Pod,
	replica loco.ReplicaStatus,
	healthyAfter time.Duration,
	now time.Time,
) loco.ProblemReason {
	var best loco.ProblemReason
	found := func(reason loco.ProblemReason) {
		if best == "" || loco.ProblemHigherPriority(reason, best) {
			best = reason
		}
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse &&
			c.Reason == corev1.PodReasonUnschedulable {
			found(loco.ProblemUnschedulable)
		}
	}
	if restartedWithin(replica, healthyAfter, now) {
		found(loco.ProblemRestarting)
		if replica.LastRestart.Cause == loco.RestartOutOfMemory {
			found(loco.ProblemOutOfMemory)
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if waiting := status.State.Waiting; waiting != nil {
			if reason := waitingProblemReason(waiting.Reason); reason != "" {
				found(reason)
			}
			if waiting.Reason == reasonCrashLoopBackOff {
				found(loco.ProblemRestarting)
			}
		}
		if exit := status.State.Terminated; exit != nil {
			found(loco.ProblemRestarting)
			if exit.Reason == reasonOOMKilled {
				found(loco.ProblemOutOfMemory)
			}
		}
		deadline := readinessFailureAt(pod, status)
		if !deadline.IsZero() && !now.Before(deadline) {
			found(loco.ProblemHealthCheckFailing)
		}
	}
	return best
}

func waitingProblemReason(reason string) loco.ProblemReason {
	switch reason {
	case reasonCreateContainerConfigError:
		return loco.ProblemConfigError
	case reasonImagePullBackOff, reasonErrImagePull, reasonInvalidImageName:
		return loco.ProblemImagePullFailed
	default:
		return ""
	}
}

func readinessFailureAt(pod corev1.Pod, status corev1.ContainerStatus) time.Time {
	if status.Ready || status.State.Running == nil {
		return time.Time{}
	}
	if !ptr.Deref(status.Started, false) {
		return time.Time{}
	}
	startedAt := status.State.Running.StartedAt
	if startedAt.IsZero() {
		return time.Time{}
	}
	for _, container := range pod.Spec.Containers {
		probe := container.ReadinessProbe
		if container.Name != status.Name || probe == nil {
			continue
		}
		grace := time.Duration(probe.InitialDelaySeconds)*time.Second +
			time.Duration(probe.PeriodSeconds)*time.Duration(probe.FailureThreshold)*time.Second
		return startedAt.Add(grace)
	}
	return time.Time{}
}
