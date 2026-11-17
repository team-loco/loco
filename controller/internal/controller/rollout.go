package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/internal/clip"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const deletedPodRetention = 2 * time.Minute

type deletedReplica struct {
	pod *corev1.Pod
	at  time.Time
}

func (r *ApplicationReconciler) recordDeletedPod(key types.NamespacedName, pod *corev1.Pod, now time.Time) {
	r.deletedMu.Lock()
	defer r.deletedMu.Unlock()
	if r.deletedPods == nil {
		r.deletedPods = make(map[types.NamespacedName]map[types.UID]deletedReplica)
	}
	if r.deletedPods[key] == nil {
		r.deletedPods[key] = make(map[types.UID]deletedReplica)
	}
	r.deletedPods[key][pod.UID] = deletedReplica{pod.DeepCopy(), now}
}
func (r *ApplicationReconciler) deletedReplicas(key types.NamespacedName, now time.Time) []corev1.Pod {
	r.deletedMu.Lock()
	defer r.deletedMu.Unlock()
	for app, pods := range r.deletedPods {
		for uid, pod := range pods {
			if now.Sub(pod.at) >= deletedPodRetention {
				delete(pods, uid)
			}
		}
		if len(pods) == 0 {
			delete(r.deletedPods, app)
		}
	}
	pods := make([]corev1.Pod, 0, len(r.deletedPods[key]))
	for _, record := range r.deletedPods[key] {
		pod := record.pod.DeepCopy()
		if pod.DeletionTimestamp == nil {
			at := metav1.NewTime(record.at)
			pod.DeletionTimestamp = &at
		}
		pods = append(pods, *pod)
	}
	return pods
}
func replicaProblem(pods []corev1.Pod, window time.Duration, now time.Time) *locov1alpha1.Problem {
	var best *locov1alpha1.Problem
	consider := func(reason locov1alpha1.ProblemReason, pod corev1.Pod, detail string) {
		if best == nil || locov1alpha1.ProblemHigherPriority(reason, best.Reason) ||
			reason == best.Reason && pod.Name < best.Replica {
			at := metav1.NewTime(now)
			best = &locov1alpha1.Problem{
				Reason:  reason,
				Replica: pod.Name,
				Detail:  clip.Bytes(detail, 1024),
				Since:   &at,
			}
		}
	}
	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			continue
		}
		reason, _ := runtimeProblemAt([]corev1.Pod{pod}, window, now)
		detail := runtimeReplicaDetail(pod, reason)
		switch reason {
		case locov1alpha1.RuntimeOutOfMemory:
			consider(locov1alpha1.ProblemOutOfMemory, pod, detail)
		case locov1alpha1.RuntimeCrashLooping:
			consider(locov1alpha1.ProblemCrashLoop, pod, detail)
		case locov1alpha1.RuntimeImagePullFailed:
			consider(locov1alpha1.ProblemImagePullFailed, pod, detail)
		case locov1alpha1.RuntimeUnschedulable:
			consider(locov1alpha1.ProblemUnschedulable, pod, detail)
		case locov1alpha1.RuntimeHealthCheckFailing:
			consider(locov1alpha1.ProblemHealthCheckFailing, pod, detail)
		}
		if stuck, ok := stuckContainer(&pod); ok {
			switch stuck.reason {
			case reasonCreateContainerConfigError:
				consider(configErrorReason(stuck.detail), pod, stuck.detail)
			case reasonErrImagePull, reasonImagePullBackOff, reasonInvalidImageName:
				consider(locov1alpha1.ProblemImagePullFailed, pod, stuck.detail)
			}
		}
	}
	return best
}

func (r *ApplicationReconciler) observeReplicas(
	ctx context.Context,
	app *locov1alpha1.Application,
	dep *appsv1ac.DeploymentApplyConfiguration,
	now time.Time,
) error {
	var listed corev1.PodList
	if err := r.List(
		ctx,
		&listed,
		client.InNamespace(r.workspaceNamespace(app)),
		client.MatchingLabels{labelApp: locov1alpha1.ApplicationName(app.Spec.ResourceID)},
	); err != nil {
		return fmt.Errorf("list rollout replicas: %w", err)
	}
	pods := make(map[types.UID]corev1.Pod)
	for _, pod := range r.deletedReplicas(client.ObjectKeyFromObject(app), now) {
		pods[pod.UID] = pod
	}
	deleted := make(map[types.UID]bool)
	for uid := range pods {
		deleted[uid] = true
	}
	for _, pod := range listed.Items {
		delete(deleted, pod.UID)
		pods[pod.UID] = pod
	}
	rollout := &locov1alpha1.RolloutStatus{
		DesiredReplicas: ptr.Deref(dep.Spec.Replicas, 0),
		Available:       rolloutAvailable(dep, app),
	}
	var current []corev1.Pod
	revision, _ := app.PlacementRevision()
	for _, pod := range pods {
		replica := replicaState(&pod, r.RuntimeRecoveryWindow, now)
		if deleted[pod.UID] && replica.State != locov1alpha1.ReplicaKilled &&
			replica.State != locov1alpha1.ReplicaStopped {
			replica.State = locov1alpha1.ReplicaStopped
			replica.StateDeadline = nil
			replica.ExitCode = nil
			replica.Detail = "no exit code seen"
		}
		if raw := pod.Annotations[locov1alpha1.KeyPlacementRevision]; raw != "" {
			podRevision, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("pod %s placement revision: %w", pod.Name, err)
			}
			replica.PlacementRevision = podRevision
		}
		replica.UID = string(pod.UID)
		replica.Name = pod.Name
		replica.Node = pod.Spec.NodeName
		replica.Version = replicaVersion(&pod, revision)
		if replica.Version == locov1alpha1.ReplicaCurrent {
			current = append(current, pod)
		}
		if app.Status.Rollout != nil {
			for _, old := range app.Status.Rollout.Replicas {
				if old.UID == replica.UID && old.State == replica.State {
					replica.StateSince = old.StateSince
				}
			}
		}
		rollout.Replicas = append(rollout.Replicas, replica)
	}
	sort.Slice(rollout.Replicas, func(i, j int) bool {
		a, b := rollout.Replicas[i], rollout.Replicas[j]
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Name < b.Name
	})
	rollout.Problem = replicaProblem(current, r.RuntimeRecoveryWindow, now)
	failure, expired := deploymentFailure(dep)
	rollout.ProgressDeadlineExceeded = expired
	if rollout.Problem == nil && expired {
		at := metav1.NewTime(now)
		rollout.Problem = &locov1alpha1.Problem{
			Reason: locov1alpha1.ProblemRolloutTimeout,
			Detail: clip.Bytes(failure.detail, 1024),
			Since:  &at,
		}
	}
	if rollout.Problem == nil {
		for _, replica := range rollout.Replicas {
			if replica.State == locov1alpha1.ReplicaKilled {
				rollout.Problem = &locov1alpha1.Problem{
					Reason:  locov1alpha1.ProblemUncleanShutdown,
					Replica: replica.Name,
					Detail:  replica.Detail,
					Since:   replica.StateSince,
				}
				break
			}
		}
	}
	if rollout.Problem != nil && app.Status.Rollout != nil && app.Status.Rollout.Problem != nil &&
		app.Status.Rollout.Problem.Reason == rollout.Problem.Reason {
		rollout.Problem.Since = app.Status.Rollout.Problem.Since
	}
	app.Status.Rollout = rollout
	return nil
}

func runtimeReplicaDetail(pod corev1.Pod, reason string) string {
	if reason == locov1alpha1.RuntimeHealthCheckFailing {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Message != "" {
				return condition.Message
			}
		}
		return detailWaitingForReadiness
	}
	if reason == locov1alpha1.RuntimeUnschedulable {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled {
				return condition.Message
			}
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if reason == locov1alpha1.RuntimeOutOfMemory {
			for _, exit := range []*corev1.ContainerStateTerminated{
				status.State.Terminated,
				status.LastTerminationState.Terminated,
			} {
				if exit != nil && exit.Reason == reasonOOMKilled {
					return fmt.Sprintf("%s · exit %d · %d restarts", exit.Reason, exit.ExitCode, status.RestartCount)
				}
			}
			continue
		}
		if waiting := status.State.Waiting; waiting != nil {
			return fmt.Sprintf("%s: %s · %d restarts", waiting.Reason, waiting.Message, status.RestartCount)
		}
		for _, exit := range []*corev1.ContainerStateTerminated{
			status.State.Terminated,
			status.LastTerminationState.Terminated,
		} {
			if exit != nil {
				return fmt.Sprintf("%s · exit %d · %d restarts", exit.Reason, exit.ExitCode, status.RestartCount)
			}
		}
	}
	return detailWaitingForReadiness
}
