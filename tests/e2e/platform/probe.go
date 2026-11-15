package platform

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const probeTimeout = 30 * time.Second

// ProbePod is a restricted pod running the public fixture image.
type ProbePod struct {
	Namespace string
	Name      string
	Labels    map[string]string
	Command   []string
	// Image defaults to the public fixture image.
	Image string
}

// Manifest renders the pod.
func (p *Platform) probeManifest(pod ProbePod) string {
	image := pod.Image
	if image == "" {
		image = p.PublicImage
	}
	labels := make([]string, 0, len(pod.Labels))
	for key, value := range pod.Labels {
		labels = append(labels, fmt.Sprintf("%q: %q", key, value))
	}
	command := make([]string, 0, len(pod.Command))
	for _, arg := range pod.Command {
		command = append(command, fmt.Sprintf("%q", arg))
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels: {%s}
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: %s
      command: [%s]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: ["ALL"]
`, pod.Name, pod.Namespace, strings.Join(labels, ", "), image, strings.Join(command, ", "))
}

// Reaches runs a pod that fetches url with wget and reports whether the fetch succeeded within
// probeTimeout. It replaces a pod of the same name.
func (p *Platform) Reaches(tb testing.TB, pod ProbePod, url string) bool {
	tb.Helper()
	pod.Command = []string{"wget", "-q", "-T", "5", "-O", "/dev/null", url}
	p.MustKubectl(tb, "-n", pod.Namespace, "delete", "pod", pod.Name, "--ignore-not-found", "--wait")
	p.MustApply(tb, p.probeManifest(pod))
	ctx, cancel := context.WithTimeout(tb.Context(), probeTimeout)
	defer cancel()
	succeeded := false
	err := Poll(ctx, func(ctx context.Context) error {
		var status corev1.Pod
		if err := p.Get(ctx, &status, "-n", pod.Namespace, "pod", pod.Name); err != nil {
			return err
		}
		switch status.Status.Phase {
		case corev1.PodSucceeded:
			succeeded = true
			return nil
		case corev1.PodFailed:
			return nil
		default:
			return fmt.Errorf("pod %s is %s", pod.Name, status.Status.Phase)
		}
	})
	if err != nil {
		tb.Logf("probe %s/%s did not finish: %v", pod.Namespace, pod.Name, err)
	}
	return succeeded
}

// StartPod runs a pod in the background, such as one to exec probes in.
func (p *Platform) StartPod(tb testing.TB, pod ProbePod) {
	tb.Helper()
	p.MustKubectl(tb, "-n", pod.Namespace, "delete", "pod", pod.Name, "--ignore-not-found", "--wait")
	p.MustApply(tb, p.probeManifest(pod))
}
