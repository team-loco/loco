package controller

import (
	"testing"
	"time"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const exitReasonError = "Error"

func TestReplicaLastExit(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	older := metav1.NewTime(now.Add(-time.Minute))
	newer := metav1.NewTime(now.Add(-time.Second))
	oom := &corev1.ContainerStateTerminated{Reason: reasonOOMKilled, ExitCode: 137, FinishedAt: older}
	crash := &corev1.ContainerStateTerminated{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}
	for _, tt := range []struct {
		name     string
		statuses []corev1.ContainerStatus
		want     *loco.ReplicaExit
	}{
		{"never exited", []corev1.ContainerStatus{{
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}, nil},
		{"previous run", []corev1.ContainerStatus{{
			RestartCount:         1,
			State:                corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			LastTerminationState: corev1.ContainerState{Terminated: oom},
		}}, &loco.ReplicaExit{Reason: reasonOOMKilled, ExitCode: 137, FinishedAt: older}},
		{"current exit is newer", []corev1.ContainerStatus{{
			RestartCount:         1,
			State:                corev1.ContainerState{Terminated: crash},
			LastTerminationState: corev1.ContainerState{Terminated: oom},
		}}, &loco.ReplicaExit{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}},
		{"newest container exit", []corev1.ContainerStatus{
			{LastTerminationState: corev1.ContainerState{Terminated: crash}},
			{LastTerminationState: corev1.ContainerState{Terminated: oom}},
		}, &loco.ReplicaExit{Reason: exitReasonError, ExitCode: 1, FinishedAt: newer}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: tt.statuses}}
			got := replicaState(pod, nil, now).LastExit
			if tt.want == nil {
				if got != nil {
					t.Fatalf("last exit = %+v, want none", got)
				}
				return
			}
			if got == nil || got.Reason != tt.want.Reason || got.ExitCode != tt.want.ExitCode ||
				!got.FinishedAt.Equal(&tt.want.FinishedAt) {
				t.Fatalf("last exit = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReplicaStartedAt(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	created := metav1.NewTime(now.Add(-time.Hour))
	started := metav1.NewTime(now.Add(-time.Minute))
	pod := &corev1.Pod{CreationTimestamp: created}
	if got := replicaState(pod, nil, now).StartedAt; got == nil || !got.Equal(&created) {
		t.Fatalf("unstarted pod started at %v, want creation time %v", got, created)
	}
	pod.Status.StartTime = &started
	if got := replicaState(pod, nil, now).StartedAt; got == nil || !got.Equal(&started) {
		t.Fatalf("started pod started at %v, want %v", got, started)
	}
}
