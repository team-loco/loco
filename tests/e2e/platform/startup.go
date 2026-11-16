package platform

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const startupPollInterval = 500 * time.Millisecond

type containerLog struct {
	pod      string
	uid      string
	name     string
	id       string
	restart  int32
	text     string
	complete bool
	err      error
}

// StartupLogs retains each observed container incarnation's logs through installation.
type StartupLogs struct {
	containers map[string]containerLog
	pods       map[types.UID]corev1.Pod
	err        error
}

// CaptureStartupLogs samples logs until cancellation, then captures a final pod inventory.
func (c *Cluster) CaptureStartupLogs(ctx context.Context, namespace, selector string) <-chan StartupLogs {
	done := make(chan StartupLogs, 1)
	go func() {
		defer close(done)
		logs := StartupLogs{containers: make(map[string]containerLog), pods: make(map[types.UID]corev1.Pod)}
		ticker := time.NewTicker(startupPollInterval)
		defer ticker.Stop()
		for {
			sampleCtx, cancel := context.WithTimeout(ctx, diagnosticTimeout)
			c.sampleStartup(sampleCtx, namespace, selector, &logs)
			cancel()
			select {
			case <-ctx.Done():
				finalCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), diagnosticBudget)
				c.sampleStartup(finalCtx, namespace, selector, &logs)
				stop()
				done <- logs
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (c *Cluster) sampleStartup(ctx context.Context, namespace, selector string, logs *StartupLogs) {
	var pods corev1.PodList
	if err := c.Get(ctx, &pods, "-n", namespace, "pods", "-l", selector); err != nil {
		logs.err = err
		return
	}
	logs.err = nil
	for _, pod := range pods.Items {
		logs.pods[pod.UID] = pod
		for _, status := range pod.Status.ContainerStatuses {
			previousID := ""
			if status.LastTerminationState.Terminated != nil {
				previousID = status.LastTerminationState.Terminated.ContainerID
			}
			for _, incarnation := range []struct {
				id       string
				restart  int32
				complete bool
			}{
				{status.ContainerID, status.RestartCount, status.State.Terminated != nil},
				{previousID, status.RestartCount - 1, true},
			} {
				if incarnation.id == "" {
					continue
				}
				id := strings.TrimPrefix(incarnation.id, "containerd://")
				text, err := c.diagnostic(ctx, "docker", "exec", c.Node(), "crictl", "logs", "--tail=200", id)
				key := string(pod.UID) + "/" + status.Name + "/" + strconv.Itoa(int(incarnation.restart))
				if previous, ok := logs.containers[key]; ok && err != nil && previous.err == nil {
					continue
				}
				logs.containers[key] = containerLog{
					pod: pod.Name, uid: string(pod.UID), name: status.Name, id: incarnation.id,
					restart: incarnation.restart, text: text, complete: incarnation.complete, err: err,
				}
			}
		}
	}
}

// CheckRestarts requires evidence for every restarted container, including missing incarnations.
func (logs StartupLogs) CheckRestarts(diagnostic string) error {
	if logs.err != nil {
		return fmt.Errorf("retrieve API pod inventory: %w", logs.err)
	}
	if len(logs.pods) == 0 {
		return errors.New("API pod inventory is empty")
	}
	for _, pod := range logs.pods {
		if len(pod.Status.ContainerStatuses) == 0 {
			return fmt.Errorf("API pod %s has no container status", pod.Name)
		}
		for _, status := range pod.Status.ContainerStatuses {
			for restart := range status.RestartCount {
				key := string(pod.UID) + "/" + status.Name + "/" + strconv.Itoa(int(restart))
				captured, ok := logs.containers[key]
				if !ok {
					return fmt.Errorf(
						"API pod %s uid %s container %s restart %d: logs unavailable",
						pod.Name,
						pod.UID,
						status.Name,
						restart,
					)
				}
				if captured.err != nil {
					return fmt.Errorf(
						"API pod %s uid %s container %s restart %d id %s: logs unavailable (retrieval failed)",
						captured.pod,
						captured.uid,
						captured.name,
						captured.restart,
						captured.id,
					)
				}
				if !strings.Contains(captured.text, diagnostic) {
					if !captured.complete {
						return fmt.Errorf(
							"API pod %s uid %s container %s restart %d id %s: logs unavailable (incomplete capture)",
							captured.pod,
							captured.uid,
							captured.name,
							captured.restart,
							captured.id,
						)
					}
					return fmt.Errorf(
						"API pod %s uid %s container %s restart %d id %s: startup logs lack the transit diagnostic",
						captured.pod,
						captured.uid,
						captured.name,
						captured.restart,
						captured.id,
					)
				}
			}
		}
	}
	return nil
}
