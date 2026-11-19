package controller

import (
	"testing"
	"time"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
)

const (
	runtimePrivateDetail = "private detail"
	testHealthyAfter     = 5 * time.Minute
)

func TestReplicaProblemReason(t *testing.T) {
	now := time.Now()
	recent := &locov1alpha1.ReplicaRestart{At: metav1.NewTime(now.Add(-time.Minute)), Cause: locov1alpha1.RestartExit}
	tests := []struct {
		name    string
		status  corev1.PodStatus
		replica locov1alpha1.ReplicaStatus
		want    locov1alpha1.ProblemReason
	}{
		{
			"backing off before a restart",
			corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reasonCrashLoopBackOff, Message: runtimePrivateDetail},
			}}}},
			locov1alpha1.ReplicaStatus{},
			locov1alpha1.ProblemRestarting,
		},
		{
			"exited",
			corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
			}}}},
			locov1alpha1.ReplicaStatus{},
			locov1alpha1.ProblemRestarting,
		},
		{
			"running after a recent restart",
			corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
			locov1alpha1.ReplicaStatus{LastRestart: recent},
			locov1alpha1.ProblemRestarting,
		},
		{
			"memory takes precedence",
			corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
			locov1alpha1.ReplicaStatus{LastRestart: &locov1alpha1.ReplicaRestart{
				At: recent.At, Cause: locov1alpha1.RestartOutOfMemory, ExitCode: 137,
			}},
			"out_of_memory",
		},
		{
			"image",
			corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}},
				},
			},
			locov1alpha1.ReplicaStatus{},
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
			locov1alpha1.ReplicaStatus{},
			"unschedulable",
		},
		{
			"quiet after the health window",
			corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
			locov1alpha1.ReplicaStatus{LastRestart: &locov1alpha1.ReplicaRestart{
				At: metav1.NewTime(now.Add(-testHealthyAfter)), Cause: locov1alpha1.RestartOutOfMemory,
			}},
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := replicaProblemReason(corev1.Pod{Status: tt.status}, tt.replica, testHealthyAfter, now)
			if reason != tt.want {
				t.Fatalf("reason = %q, want %q", reason, tt.want)
			}
		})
	}
}

func TestRuntimePodRoutesToApplication(t *testing.T) {
	application := testApplication()
	deployment := desiredDeployment(
		application,
		testEnvironmentNamespace(application),
		envSecretMount{},
		testTelemetry(),
		testMinReadySeconds,
		testDrainSeconds,
	)
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
	reason := replicaProblemReason(pod, locov1alpha1.ReplicaStatus{}, testHealthyAfter, time.Now())
	if reason != "" {
		t.Fatalf("startup reported %q", reason)
	}
	pod.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(time.Now().Add(-time.Hour))
	reason = replicaProblemReason(pod, locov1alpha1.ReplicaStatus{}, testHealthyAfter, time.Now())
	if reason != locov1alpha1.ProblemHealthCheckFailing {
		t.Fatalf("unready after grace reported %q", reason)
	}
}
