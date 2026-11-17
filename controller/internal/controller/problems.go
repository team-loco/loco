package controller

import (
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
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

func replicaProblemReason(pod corev1.Pod, recoveryWindow time.Duration, now time.Time) locov1alpha1.ProblemReason {
	var best locov1alpha1.ProblemReason
	found := func(reason locov1alpha1.ProblemReason) {
		if best == "" || locov1alpha1.ProblemHigherPriority(reason, best) {
			best = reason
		}
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse &&
			c.Reason == corev1.PodReasonUnschedulable {
			found(locov1alpha1.ProblemUnschedulable)
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		running := status.State.Running
		recovering := replicaRecoveringAt(pod, status, recoveryWindow, now)
		waiting := status.State.Waiting
		if waiting != nil {
			if reason := waitingProblemReason(waiting.Reason, waiting.Message); reason != "" {
				found(reason)
			}
		}
		backoff := waiting != nil && waiting.Reason == reasonCrashLoopBackOff
		lastExit := status.LastTerminationState.Terminated
		activeExit := status.State.Terminated
		if lastExit != nil && (backoff || recovering) {
			found(locov1alpha1.ProblemCrashLoop)
			if lastExit.Reason == reasonOOMKilled {
				found(locov1alpha1.ProblemOutOfMemory)
			}
		}
		if activeExit != nil {
			if status.RestartCount > 0 {
				found(locov1alpha1.ProblemCrashLoop)
			}
			if activeExit.Reason == reasonOOMKilled {
				found(locov1alpha1.ProblemOutOfMemory)
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
					found(locov1alpha1.ProblemHealthCheckFailing)
				}
			}
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

func waitingProblemReason(reason, detail string) locov1alpha1.ProblemReason {
	switch reason {
	case reasonCreateContainerConfigError:
		return configErrorReason(detail)
	case reasonImagePullBackOff, reasonErrImagePull, reasonInvalidImageName:
		return locov1alpha1.ProblemImagePullFailed
	case reasonCrashLoopBackOff:
		return locov1alpha1.ProblemCrashLoop
	default:
		return ""
	}
}

func configErrorReason(detail string) locov1alpha1.ProblemReason {
	if strings.Contains(detail, kubeletRootImage) || strings.Contains(detail, kubeletNonNumericUser) {
		return locov1alpha1.ProblemImageUser
	}
	return locov1alpha1.ProblemConfigError
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
