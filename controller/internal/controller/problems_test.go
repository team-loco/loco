package controller

import (
	"testing"
	"time"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
)

const runtimePrivateDetail = "private detail"

func TestReplicaProblemReason(t *testing.T) {
	tests := []struct {
		name   string
		status corev1.PodStatus
		want   locov1alpha1.ProblemReason
	}{
		{
			"crash",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason:  "CrashLoopBackOff",
								Message: runtimePrivateDetail,
							},
						},
					},
				},
			},
			"crash_loop",
		},
		{
			"memory takes precedence",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
						},
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled},
						},
					},
				},
			},
			"out_of_memory",
		},
		{
			"image",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}},
				},
			},
			"image_pull_failed",
		},
		{
			"capacity",
			corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  corev1.PodReasonUnschedulable,
						Message: runtimePrivateDetail,
					},
				},
			},
			"unschedulable",
		},
		{
			"healthy ignores old oom",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Ready: true,
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled},
						},
					},
				},
			},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := replicaProblemReason(corev1.Pod{Status: tt.status}, time.Minute, time.Now())
			if reason != tt.want {
				t.Fatalf("reason = %q, want %q", reason, tt.want)
			}
		})
	}
}

func TestRuntimePodRoutesToApplication(t *testing.T) {
	application := testApplication()
	deployment, err := desiredDeployment(
		application,
		testEnvironmentNamespace(application),
		envSecretMount{},
		testTelemetry(),
		testMinReadySeconds,
		testDrainSeconds,
	)
	if err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{Annotations: deployment.Spec.Template.Annotations}
	requests := applicationForObject(t.Context(), pod)
	if len(requests) != 1 || requests[0].Name != application.Name || requests[0].Namespace != application.Namespace {
		t.Fatalf("pod watch requests = %v", requests)
	}
}

func TestRuntimeReadinessAllowsTheConfiguredStartupWindow(t *testing.T) {
	started := true
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{
			Name:           "runtime-service",
			ReadinessProbe: &corev1.Probe{InitialDelaySeconds: 60, PeriodSeconds: 10, FailureThreshold: 3},
		},
	}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			Name:    "runtime-service",
			Started: &started,
			State:   corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		},
	}}}
	reason := replicaProblemReason(pod, time.Minute, time.Now())
	if reason != "" {
		t.Fatalf("startup reported %q", reason)
	}
	pod.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(time.Now().Add(-time.Hour))
	reason = replicaProblemReason(pod, time.Minute, time.Now())
	if reason != locov1alpha1.ProblemHealthCheckFailing {
		t.Fatalf("unready after grace reported %q", reason)
	}
}

func TestRuntimeCrashPersistsWhileAnUnreadyReplicaRestarts(t *testing.T) {
	pod := corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			RestartCount:         4,
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		},
	}}}
	for _, state := range []corev1.ContainerState{
		{Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff}},
		{Running: &corev1.ContainerStateRunning{}},
		{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	} {
		pod.Status.ContainerStatuses[0].State = state
		reason := replicaProblemReason(pod, time.Minute, time.Now())
		if reason != locov1alpha1.ProblemCrashLoop {
			t.Fatalf("restart state %+v cleared the crash: %q", state, reason)
		}
	}
}

func TestRuntimeProcessOnlyCrashRequiresASustainedRunToRecover(t *testing.T) {
	pod := corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
		Ready: true, RestartCount: 5,
		State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
	}}}}
	reason := replicaProblemReason(pod, time.Minute, time.Now())
	if reason != locov1alpha1.ProblemCrashLoop {
		t.Fatalf("brief ready run cleared crash: %q", reason)
	}
	pod.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(time.Now().Add(-time.Hour))
	reason = replicaProblemReason(pod, time.Minute, time.Now())
	if reason != "" {
		t.Fatalf("sustained recovery reported %q", reason)
	}
}

func TestRuntimeRecoveredExitDoesNotOverrideALaterHealthFailure(t *testing.T) {
	started := true
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "recovered-service", ReadinessProbe: &corev1.Probe{PeriodSeconds: 10, FailureThreshold: 3},
	}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
		{
			Name:         "recovered-service",
			Started:      &started,
			RestartCount: 4,
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{StartedAt: metav1.NewTime(time.Now().Add(-time.Hour))},
			},
			LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{Reason: reasonOOMKilled, ExitCode: 137},
			},
		},
	}}}
	reason := replicaProblemReason(pod, time.Minute, time.Now())
	if reason != locov1alpha1.ProblemHealthCheckFailing {
		t.Fatalf("historical exit overrode health failure: %q", reason)
	}
}

func TestUnreadyCrashDoesNotRecoverBetweenSlowLivenessRestarts(t *testing.T) {
	startedAt := time.Now().Add(-70 * time.Second)
	pod := corev1.Pod{Status: corev1.PodStatus{
		Conditions: []corev1.PodCondition{{
			Type: corev1.ContainersReady, Status: corev1.ConditionFalse,
			LastTransitionTime: metav1.NewTime(startedAt.Add(-time.Minute)),
		}},
		ContainerStatuses: []corev1.ContainerStatus{{
			RestartCount: 1,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{
				StartedAt: metav1.NewTime(startedAt),
			}},
			LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
		}},
	}}
	reason := replicaProblemReason(pod, time.Minute, time.Now())
	if reason != locov1alpha1.ProblemCrashLoop {
		t.Fatalf("unready retry cleared the crash after the recovery window: %q", reason)
	}
	pod.Status.ContainerStatuses[0].Ready = true
	reason = replicaProblemReason(pod, time.Minute, time.Now())
	if reason != "" {
		t.Fatalf("sustained ready run retained crash: %q", reason)
	}
	// A new failure after it became Ready has a transition newer than its run.
	pod.Status.ContainerStatuses[0].Ready = false
	pod.Status.Conditions[0].LastTransitionTime = metav1.NewTime(startedAt.Add(time.Minute))
	reason = replicaProblemReason(pod, time.Minute, time.Now())
	if reason != "" {
		t.Fatalf("later unreadiness revived historical crash: %q", reason)
	}
	pod.Status.Conditions[0].LastTransitionTime = metav1.Time{}
	reason = replicaProblemReason(pod, time.Minute, time.Now())
	if reason != "" {
		t.Fatalf("missing readiness history revived historical crash: %q", reason)
	}
}
