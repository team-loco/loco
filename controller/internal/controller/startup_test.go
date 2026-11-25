package controller

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestStartupFailedRevision(t *testing.T) {
	for _, tt := range []struct {
		reason, detail string
		want           loco.ProblemReason
	}{
		{reasonCreateContainerConfigError, "secret key missing", loco.ProblemConfigError},
		{reasonCreateContainerConfigError, "image will run as root", loco.ProblemImageUser},
		{reasonCreateContainerConfigError, "non-numeric user", loco.ProblemImageUser},
		{"InvalidImageName", "invalid reference format", loco.ProblemImagePullFailed},
		{"ErrImagePull", "registry unavailable", ""},
		{"ImagePullBackOff", "registry unavailable", ""},
		{"CreateContainerError", "runtime unavailable", ""},
		{"RunContainerError", "runtime unavailable", ""},
		{"ContainerCreating", "", ""},
		{"CrashLoopBackOff", "back-off restarting", ""},
	} {
		t.Run(tt.reason+"/"+tt.detail, func(t *testing.T) {
			now := time.Now()
			replicas := []observedReplica{{pod: corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: tt.reason, Message: tt.detail},
				}}},
			}}, status: loco.ReplicaStatus{Name: "broken"}}}
			got := startupFailedRevision(replicas, 2, now)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("unexpected stop: %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("no stop for permanent startup failure")
			}
			if got.Reason != tt.want || got.PlacementRevision != 2 {
				t.Fatalf("stop = %+v", got)
			}
			if got.Replica != "broken" || !got.At.Time.Equal(now) {
				t.Fatalf("stop = %+v", got)
			}
			if !strings.Contains(got.Detail, tt.reason+": "+tt.detail) {
				t.Fatalf("detail = %q", got.Detail)
			}
		})
	}
}
