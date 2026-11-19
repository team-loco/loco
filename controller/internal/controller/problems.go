package controller

import (
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	reasonOOMKilled                  = "OOMKilled"
	reasonProgressDeadlineExceeded   = "ProgressDeadlineExceeded"
	reasonCreateContainerConfigError = "CreateContainerConfigError"
	reasonImagePullBackOff           = "ImagePullBackOff"
	reasonErrImagePull               = "ErrImagePull"
	reasonInvalidImageName           = "InvalidImageName"
	reasonCrashLoopBackOff           = "CrashLoopBackOff"
	kubeletRootImage                 = "image will run as root"
	kubeletNonNumericUser            = "non-numeric user"
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
			if reason := waitingProblemReason(waiting.Reason, waiting.Message); reason != "" {
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
		if status.Ready {
			continue
		}

		if status.State.Running != nil && ptr.Deref(status.Started, false) {
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
					found(loco.ProblemHealthCheckFailing)
				}
			}
		}
	}
	return best
}

func waitingProblemReason(reason, detail string) loco.ProblemReason {
	switch reason {
	case reasonCreateContainerConfigError:
		return configErrorReason(detail)
	case reasonImagePullBackOff, reasonErrImagePull, reasonInvalidImageName:
		return loco.ProblemImagePullFailed
	default:
		return ""
	}
}

func configErrorReason(detail string) loco.ProblemReason {
	if strings.Contains(detail, kubeletRootImage) || strings.Contains(detail, kubeletNonNumericUser) {
		return loco.ProblemImageUser
	}
	return loco.ProblemConfigError
}

func deploymentFailure(dep *appsv1ac.DeploymentApplyConfiguration) (string, bool) {
	if dep.Status == nil {
		return "", false
	}
	generation := ptr.Deref(dep.Generation, 0)
	if generation <= 0 || ptr.Deref(dep.Status.ObservedGeneration, 0) != generation {
		return "", false
	}
	for _, condition := range dep.Status.Conditions {
		if ptr.Deref(condition.Type, "") == appsv1.DeploymentProgressing &&
			ptr.Deref(condition.Status, "") == corev1.ConditionFalse &&
			ptr.Deref(condition.Reason, "") == reasonProgressDeadlineExceeded {
			return ptr.Deref(condition.Message, ""), true
		}
	}
	return "", false
}
