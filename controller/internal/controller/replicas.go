package controller

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"

	"github.com/team-loco/loco/internal/clip"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const replicaDetailBytes = 512

const (
	detailWaitingForReadiness = "waiting for readiness"
	reasonContainerCreating   = "ContainerCreating"
	reasonPodInitializing     = "PodInitializing"
)

func replicaState(pod *corev1.Pod, pull *pullEvent, recoveryWindow time.Duration, now time.Time) loco.ReplicaStatus {
	result := classifyReplica(pod, pull, recoveryWindow, now)
	result.Detail = clip.Bytes(result.Detail, replicaDetailBytes)
	return result
}

func classifyReplica(pod *corev1.Pod, pull *pullEvent, recoveryWindow time.Duration, now time.Time) loco.ReplicaStatus {
	result := loco.ReplicaStatus{State: loco.ReplicaUnknown, StateSince: pod.CreationTimestamp.DeepCopy()}
	for _, status := range pod.Status.ContainerStatuses {
		result.Restarts += status.RestartCount
		if exit := status.LastTerminationState.Terminated; exit != nil {
			result.ExitCode = new(exit.ExitCode)
		}
		if exit := status.State.Terminated; exit != nil {
			result.ExitCode = new(exit.ExitCode)
		}
	}
	if pod.DeletionTimestamp != nil {
		return shutdownOutcome(pod, now, result)
	}

	for _, status := range pod.Status.ContainerStatuses {
		waiting := status.State.Waiting
		if waiting != nil {
			if waitingProblemReason(waiting.Reason, waiting.Message) != "" {
				result.State = loco.ReplicaFailing
				result.Detail = waiting.Reason + ": " + waiting.Message
				return result
			}
		}
		if status.State.Terminated != nil && status.RestartCount > 0 ||
			status.LastTerminationState.Terminated != nil && replicaRecoveringAt(*pod, status, recoveryWindow, now) {
			result.State = loco.ReplicaFailing
			result.Detail = fmt.Sprintf("restarting · %d restarts", result.Restarts)
			return result
		}
	}
	scheduled := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled {
			scheduled = condition.Status == corev1.ConditionTrue
			if !scheduled {
				result.Detail = condition.Message
				result.StateSince = condition.LastTransitionTime.DeepCopy()
			}
		}
	}
	if !scheduled {
		result.State = loco.ReplicaScheduling
		return result
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Waiting != nil || status.State.Running != nil && !status.Ready {
			result.State = loco.ReplicaStarting
			if status.State.Waiting != nil {
				result.Detail = status.State.Waiting.Message
				reason := status.State.Waiting.Reason
				if (reason == reasonContainerCreating || reason == reasonPodInitializing) && pull != nil &&
					pull.reason == pullReasonPulling {
					result.State = loco.ReplicaPulling
					result.StateSince = pull.at.DeepCopy()
				}
			} else {
				result.StateSince = status.State.Running.StartedAt.DeepCopy()
				result.Detail = detailWaitingForReadiness
			}
			return result
		}
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			result.StateSince = condition.LastTransitionTime.DeepCopy()
			switch condition.Status {
			case corev1.ConditionTrue:
				result.State = loco.ReplicaServing
			case corev1.ConditionFalse:
				result.State = loco.ReplicaStarting
			}
			result.Detail = condition.Message
			return result
		}
	}
	result.Detail = string(pod.Status.Phase) + ": " + pod.Status.Reason + " " + pod.Status.Message
	return result
}

func rolloutAvailable(dep *appsv1ac.DeploymentApplyConfiguration, app *loco.Application) bool {
	revision, ok := app.PlacementRevision()
	return ok && app.Status.ObservedPlacementRevision == revision &&
		deploymentReady(dep, ptr.Deref(dep.Spec.Replicas, 0))
}

func shutdownOutcome(pod *corev1.Pod, now time.Time, result loco.ReplicaStatus) loco.ReplicaStatus {
	deadline := pod.DeletionTimestamp.Time
	grace := time.Duration(ptr.Deref(pod.DeletionGracePeriodSeconds, int64(0))) * time.Second
	request := deadline.Add(-grace)
	drainEnd := request
	for _, container := range pod.Spec.Containers {
		if container.Lifecycle != nil && container.Lifecycle.PreStop != nil &&
			container.Lifecycle.PreStop.Sleep != nil {
			end := request.Add(time.Duration(container.Lifecycle.PreStop.Sleep.Seconds) * time.Second)
			if end.After(drainEnd) {
				drainEnd = end
			}
		}
	}
	result.StateSince = new(metav1.NewTime(request))
	allTerminated := true
	for _, status := range pod.Status.ContainerStatuses {
		exit := status.State.Terminated
		if exit == nil {
			allTerminated = status.State.Running == nil && allTerminated
			continue
		}
		if exit.ExitCode == 137 && exit.Reason != reasonOOMKilled &&
			!exit.FinishedAt.Before(&metav1.Time{Time: deadline.Add(-time.Second)}) {
			result.State = loco.ReplicaKilled
			result.StateSince = exit.FinishedAt.DeepCopy()
			result.Detail = fmt.Sprintf("SIGKILL at the %.0f s grace limit", grace.Seconds())
			return result
		}
	}
	if allTerminated {
		result.State = loco.ReplicaStopped
		result.Detail = "no exit code seen"
		for _, status := range pod.Status.ContainerStatuses {
			exit := status.State.Terminated
			if exit == nil {
				continue
			}
			result.StateSince = exit.FinishedAt.DeepCopy()
			result.Detail = fmt.Sprintf(
				"exit %d · %.0f s after the drain",
				exit.ExitCode,
				exit.FinishedAt.Sub(drainEnd).Seconds(),
			)
			if exit.Reason == reasonOOMKilled {
				result.Detail = reasonOOMKilled
			}
		}
		return result
	}
	result.State = loco.ReplicaStopping
	end := deadline
	result.Detail = "SIGTERM sent"
	if now.Before(drainEnd) {
		end = drainEnd
		result.Detail = "draining"
	}
	result.StateDeadline = new(metav1.NewTime(end))
	return result
}
