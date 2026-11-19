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

// observedReplica pairs a pod with the replica status observed from it.
type observedReplica struct {
	pod    corev1.Pod
	status locov1alpha1.ReplicaStatus
}

func replicaProblem(
	replicas []observedReplica,
	policy locov1alpha1.RestartPolicySpec,
	healthyAfter time.Duration,
	now time.Time,
) *locov1alpha1.Problem {
	var best *locov1alpha1.Problem
	for _, replica := range replicas {
		reason := replicaProblemReason(replica.pod, replica.status, healthyAfter, now)
		if reason == "" {
			continue
		}
		if best == nil || locov1alpha1.ProblemHigherPriority(reason, best.Reason) ||
			reason == best.Reason && replica.pod.Name < best.Replica {
			at := metav1.NewTime(now)
			best = &locov1alpha1.Problem{
				Reason:  reason,
				Replica: replica.pod.Name,
				Detail:  clip.Bytes(replicaProblemDetail(replica, reason, policy), 1024),
				Since:   &at,
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
	listed, events, err := r.listRolloutPods(ctx, app)
	if err != nil {
		return err
	}
	pods := make(map[types.UID]corev1.Pod)
	for _, pod := range r.deletedReplicas(client.ObjectKeyFromObject(app), now) {
		pods[pod.UID] = pod
	}
	deleted := make(map[types.UID]bool)
	for uid := range pods {
		deleted[uid] = true
	}
	for _, pod := range listed {
		delete(deleted, pod.UID)
		pods[pod.UID] = pod
	}
	policy := app.Spec.ServiceSpec.RestartPolicy
	previous := app.Status.Rollout
	if previous == nil {
		previous = &locov1alpha1.RolloutStatus{}
	}
	rollout := &locov1alpha1.RolloutStatus{
		DesiredReplicas: ptr.Deref(dep.Spec.Replicas, 0),
		Available:       rolloutAvailable(dep, app),
		Stopped:         stoppedRevision(app),
	}
	if rollout.Stopped != nil && rollout.DesiredReplicas == 0 {
		rollout.DesiredReplicas = previous.DesiredReplicas
		if resources := app.Spec.ServiceSpec.Resources; !resources.Scalers.Enabled {
			rollout.DesiredReplicas = resources.Replicas.Min
		}
	}
	var current []observedReplica
	revision, placed := app.PlacementRevision()
	for _, pod := range pods {
		podEvents := events[pod.UID]
		replica, err := observeReplica(&pod, &podEvents, deleted[pod.UID], previous, policy, now)
		if err != nil {
			return err
		}
		if placed && replica.PlacementRevision == revision && pod.DeletionTimestamp == nil {
			current = append(current, observedReplica{pod: pod, status: replica})
		}
		rollout.Replicas = append(rollout.Replicas, replica)
	}
	sort.Slice(rollout.Replicas, func(i, j int) bool { return rollout.Replicas[i].Name < rollout.Replicas[j].Name })
	sort.Slice(current, func(i, j int) bool { return current[i].pod.Name < current[j].pod.Name })
	rollout.ProvenRevision = previous.ProvenRevision
	if rollout.Stopped == nil {
		rollout.Stopped = exhaustedRevision(current, revision, rollout.ProvenRevision, policy, now)
	}
	r.observeHealth(rollout, current, revision, now)
	rollout.Problem = replicaProblem(current, policy, r.HealthyAfter, now)
	if stopped := rollout.Stopped; stopped != nil {
		rollout.Problem = &locov1alpha1.Problem{
			Reason:  locov1alpha1.ProblemRestartsExhausted,
			Replica: stopped.Replica,
			Detail:  stoppedDetail(stopped, policy),
			Since:   stopped.At.DeepCopy(),
		}
	}
	failure, expired := deploymentFailure(dep)
	rollout.ProgressDeadlineExceeded = expired
	if rollout.Problem == nil && expired {
		at := metav1.NewTime(now)
		rollout.Problem = &locov1alpha1.Problem{
			Reason: locov1alpha1.ProblemRolloutTimeout,
			Detail: clip.Bytes(failure, 1024),
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
	if rollout.Problem != nil && previous.Problem != nil && previous.Problem.Reason == rollout.Problem.Reason {
		rollout.Problem.Since = previous.Problem.Since
	}
	app.Status.Rollout = rollout
	return nil
}

func observeReplica(
	pod *corev1.Pod,
	events *podEvent,
	deleted bool,
	previous *locov1alpha1.RolloutStatus,
	policy locov1alpha1.RestartPolicySpec,
	now time.Time,
) (locov1alpha1.ReplicaStatus, error) {
	replica := replicaState(pod, events, now)
	if deleted && replica.State != locov1alpha1.ReplicaKilled && replica.State != locov1alpha1.ReplicaStopped {
		replica.State = locov1alpha1.ReplicaStopped
		replica.StateDeadline = nil
		replica.Detail = ""
		if replica.LastExit == nil {
			replica.Detail = "no exit code seen"
		}
	}
	if raw := pod.Annotations[locov1alpha1.KeyPlacementRevision]; raw != "" {
		podRevision, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return replica, fmt.Errorf("pod %s placement revision: %w", pod.Name, err)
		}
		replica.PlacementRevision = podRevision
	}
	replica.UID = string(pod.UID)
	replica.Name = pod.Name
	replica.Node = pod.Spec.NodeName
	var old *locov1alpha1.ReplicaStatus
	for i := range previous.Replicas {
		if previous.Replicas[i].UID == replica.UID {
			old = &previous.Replicas[i]
		}
	}
	if old != nil && old.State == replica.State && replica.State != locov1alpha1.ReplicaPulling {
		replica.StateSince = old.StateSince
	}
	trackRestarts(&replica, old, policy, now)
	return replica, nil
}

// observeHealth marks an available rollout healthy once no current replica restarted within
// HealthyAfter, and its revision proven once every current replica has also served that long.
func (r *ApplicationReconciler) observeHealth(
	rollout *locov1alpha1.RolloutStatus,
	current []observedReplica,
	revision int64,
	now time.Time,
) {
	if rollout.Stopped != nil {
		rollout.Available = false
		return
	}
	if !rollout.Available {
		return
	}
	var newest time.Time
	for _, replica := range current {
		if restartedWithin(replica.status, r.HealthyAfter, now) && replica.status.LastRestart.At.After(newest) {
			newest = replica.status.LastRestart.At.Time
		}
	}
	rollout.Healthy = newest.IsZero()
	if !rollout.Healthy {
		rollout.HealthyAt = new(metav1.NewTime(newest.Add(r.HealthyAfter)))
		return
	}
	for _, replica := range current {
		since := replica.status.StateSince
		if replica.status.State != locov1alpha1.ReplicaServing || since == nil || now.Sub(since.Time) < r.HealthyAfter {
			return
		}
	}
	if len(current) > 0 {
		rollout.ProvenRevision = revision
	}
}

func replicaProblemDetail(
	replica observedReplica,
	reason locov1alpha1.ProblemReason,
	policy locov1alpha1.RestartPolicySpec,
) string {
	pod := replica.pod
	if reason == locov1alpha1.ProblemRestarting {
		restart := replica.status.LastRestart
		for _, status := range pod.Status.ContainerStatuses {
			if exit := status.State.Terminated; exit != nil {
				restart = &locov1alpha1.ReplicaRestart{Cause: locov1alpha1.RestartExit, ExitCode: exit.ExitCode}
			}
		}
		if restart != nil {
			return restartingDetail(restart, len(replica.status.RestartsAt), policy)
		}
	}
	if reason == locov1alpha1.ProblemHealthCheckFailing {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Message != "" {
				return condition.Message
			}
		}
		return detailWaitingForReadiness
	}
	if reason == locov1alpha1.ProblemUnschedulable {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled {
				return condition.Message
			}
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if reason == locov1alpha1.ProblemOutOfMemory {
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
