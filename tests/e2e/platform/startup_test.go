package platform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
)

const startupPodUID = "pod-uid"

func TestStartupLogsRetainContainerEvidence(t *testing.T) {
	const diagnostic = "verify the transit keys"
	const firstContainer = "containerd://first"
	for _, tc := range []struct {
		name, text, want string
		captureEarly     bool
		complete         bool
	}{
		{"retained", diagnostic, "", true, false},
		{"unavailable", diagnostic, "logs unavailable", false, false},
		{"unexplained", "unrelated startup failure", "startup logs lack the transit diagnostic", true, true},
		{"incomplete", "starting up", "logs unavailable (incomplete capture)", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := fakeCluster(t, false)
			dir := t.TempDir()
			podsPath := filepath.Join(dir, "pods.json")
			logsPath := filepath.Join(dir, "container.log")
			failPath := filepath.Join(dir, "fail-logs")
			c.env = append(c.env, "PODS_FILE="+podsPath, "LOG_FILE="+logsPath, "FAIL_LOGS="+failPath)
			pod := corev1.Pod{
				Name: apiProcess, UID: startupPodUID,
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{{Name: apiProcess, ContainerID: firstContainer}},
				},
			}
			writePods := func() {
				t.Helper()
				data, err := json.Marshal(corev1.PodList{Items: []corev1.Pod{pod}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(podsPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.complete {
				pod.Status.ContainerStatuses[0].State.Terminated = &corev1.ContainerStateTerminated{
					ContainerID: firstContainer,
				}
			}
			writePods()
			if err := os.WriteFile(logsPath, []byte(tc.text), 0o600); err != nil {
				t.Fatal(err)
			}
			logs := StartupLogs{containers: make(map[string]containerLog), pods: make(map[types.UID]corev1.Pod)}
			if tc.captureEarly {
				c.sampleStartup(t.Context(), "fixtures", "app=api", &logs)
			}
			pod.Status.ContainerStatuses[0].ContainerID = "containerd://second"
			pod.Status.ContainerStatuses[0].RestartCount = 1
			pod.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
				ContainerID: firstContainer,
			}
			writePods()
			if err := os.WriteFile(failPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			c.sampleStartup(t.Context(), "fixtures", "app=api", &logs)
			err := logs.CheckRestarts(diagnostic)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestStartupLogsRejectMissingRestarts(t *testing.T) {
	logs := StartupLogs{pods: map[types.UID]corev1.Pod{startupPodUID: {
		Name: apiProcess, UID: startupPodUID,
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: apiProcess, RestartCount: 2}}},
	}}}
	if err := logs.CheckRestarts(
		"transit",
	); err == nil ||
		!strings.Contains(err.Error(), "restart 0: logs unavailable") {
		t.Fatalf("error = %v, want missing first incarnation", err)
	}
}

func TestStartupCaptureFinishesAfterCancellation(t *testing.T) {
	c, _ := fakeCluster(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	capture := c.CaptureStartupLogs(ctx, "fixtures", "app=api")
	cancel()
	select {
	case logs := <-capture:
		if logs.err != nil {
			t.Fatalf("final inventory: %v", logs.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup capture did not finish")
	}
}
