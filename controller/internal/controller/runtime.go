package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const reasonOOMKilled = "OOMKilled"

// Priority is independent of API list order when replicas have different problems.
func runtimeProblem(pods []corev1.Pod, recoveryWindow time.Duration) (string, string) {
	reasons := []string{
		locov1alpha1.RuntimeOutOfMemory,
		locov1alpha1.RuntimeCrashLooping,
		locov1alpha1.RuntimeImagePullFailed,
		locov1alpha1.RuntimeUnschedulable,
		locov1alpha1.RuntimeHealthCheckFailing,
	}
	found := map[string]bool{}
	for _, pod := range pods {
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse &&
				c.Reason == corev1.PodReasonUnschedulable {
				found[locov1alpha1.RuntimeUnschedulable] = true
			}
		}
		for _, status := range pod.Status.ContainerStatuses {
			// A restarted process can briefly be Ready on each retry without a
			// readiness probe. Retained exit history only describes this episode
			// while its current run has not survived the recovery window.
			running := status.State.Running
			recovering := running != nil &&
				(running.StartedAt.IsZero() || time.Since(running.StartedAt.Time) < recoveryWindow)
			waiting := status.State.Waiting
			backoff := waiting != nil && waiting.Reason == reasonCrashLoopBackOff
			lastExit := status.LastTerminationState.Terminated
			activeExit := status.State.Terminated
			if lastExit != nil && (backoff || recovering) {
				found[locov1alpha1.RuntimeCrashLooping] = true
				if lastExit.Reason == reasonOOMKilled {
					found[locov1alpha1.RuntimeOutOfMemory] = true
				}
			}
			if activeExit != nil {
				if status.RestartCount > 0 {
					found[locov1alpha1.RuntimeCrashLooping] = true
				}
				if activeExit.Reason == reasonOOMKilled {
					found[locov1alpha1.RuntimeOutOfMemory] = true
				}
			}
			if status.Ready {
				continue
			}
			if waiting := status.State.Waiting; waiting != nil {
				switch waiting.Reason {
				case reasonCrashLoopBackOff:
					found[locov1alpha1.RuntimeCrashLooping] = true
				case reasonImagePullBackOff, reasonErrImagePull, reasonInvalidImageName:
					found[locov1alpha1.RuntimeImagePullFailed] = true
				}
			}
			if running != nil && ptr.Deref(status.Started, false) {
				for _, container := range pod.Spec.Containers {
					probe := container.ReadinessProbe
					if container.Name != status.Name || probe == nil {
						continue
					}
					// Initial readiness is false before any probe has run. Allow the
					// configured grace and failure window before reporting a problem.
					grace := time.Duration(probe.InitialDelaySeconds)*time.Second +
						time.Duration(probe.PeriodSeconds)*time.Duration(probe.FailureThreshold)*time.Second
					startedAt := status.State.Running.StartedAt
					if !startedAt.IsZero() && time.Since(startedAt.Time) >= grace {
						found[locov1alpha1.RuntimeHealthCheckFailing] = true
					}
				}
			}
		}
	}
	for _, reason := range reasons {
		if found[reason] {
			message, _ := locov1alpha1.RuntimeProblemMessage(reason)
			return reason, message
		}
	}
	return "", ""
}

func (r *ApplicationReconciler) observeRuntimeProblem(
	ctx context.Context,
	app *locov1alpha1.Application,
	envRevision string,
) error {
	var pods corev1.PodList
	if err := r.List(
		ctx,
		&pods,
		client.InNamespace(r.workspaceNamespace(app)),
		client.MatchingLabels{labelApp: locov1alpha1.ApplicationName(app.Spec.ResourceID)},
	); err != nil {
		return fmt.Errorf("list runtime replicas: %w", err)
	}
	current := make([]corev1.Pod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if !runsCurrentTemplate(&pod, app.Spec.ServiceSpec.Deployment.Image, envRevision) {
			continue
		}
		if revision, ok := app.PlacementRevision(); ok &&
			pod.Annotations[locov1alpha1.KeyPlacementRevision] != strconv.FormatInt(revision, 10) {
			continue
		}
		current = append(current, pod)
	}
	app.Status.RuntimeReason, app.Status.RuntimeMessage = runtimeProblem(current, r.RuntimeRecoveryWindow)
	return nil
}
