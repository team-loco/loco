package apps

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestPortRollout(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-port-rollout"
	host := w.Host(app)
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage, host)
	service.Regions[p.Region] = locofile.Region{
		CPU: "100m", Memory: "128Mi",
		Replicas: &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))},
	}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	id := w.ResourceID(t, app)

	// This unlabelled pod is outside the workspace and cannot use the gateway-only ingress rule.
	// Verify enforcement before using a passing request stream as evidence for the policy change.
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", "port-rollout-denied")
	if p.Reaches(t, platform.ProbePod{Namespace: p.Namespace, Name: "port-rollout-denied"}, serviceURL(w, id)) {
		t.Fatal("a pod outside the workspace bypasses gateway ingress isolation")
	}
	var denied corev1.Pod
	p.MustGet(t, &denied, "-n", p.Namespace, "pod", "port-rollout-denied")
	if denied.Status.Phase != corev1.PodFailed || len(denied.Status.ContainerStatuses) != 1 ||
		denied.Status.ContainerStatuses[0].State.Terminated == nil ||
		denied.Status.ContainerStatuses[0].State.Terminated.ExitCode != 1 {
		t.Fatalf("the denied wget did not run and exit with failure: %+v", denied.Status.ContainerStatuses)
	}
	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	public := startPortPoller(t, p.Namespace, "port-rollout-public",
		fmt.Sprintf("http://%s.%s.svc.cluster.local/", gateway, p.Namespace), host)
	private := startPortPoller(t, w.Namespace, "port-rollout-private", "http://"+app+"/", "")
	pollers := []func() portTraffic{public, private}
	baseline := []portTraffic{public(), private()}
	previous := append([]portTraffic(nil), baseline...)
	checkpoint := func(phase string, progress bool) {
		t.Helper()
		for i, poller := range pollers {
			got := poller()
			t.Logf("%s %s stream=%d success=%d failures=%d", time.Now().UTC().Format(time.RFC3339Nano),
				phase, i, got.success, got.failures)
			if progress && got.success <= previous[i].success {
				t.Errorf(
					"%s stream %d stopped serving (%d before, %d after)",
					phase, i, previous[i].success, got.success,
				)
			}
			if failed := got.failures - baseline[i].failures; failed != 0 {
				t.Errorf("%s stream %d failed %d requests", phase, i, failed)
			}
			previous[i] = got
		}
	}
	recordPortTransitions(t, w.Namespace, app, "app=resource-"+id)

	checkpoint("before successful port change", false)
	service.Image = p.AlternatePortImage
	service.Port = new(int32(9090))
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	checkpoint("after successful port change", true)
	for _, pod := range appPods(t, w, "app=resource-"+id) {
		if pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil &&
			pod.Spec.Containers[0].Ports[0].ContainerPort != 9090 {
			t.Errorf(
				"completed rollout retains pod %s on port %d",
				pod.Name, pod.Spec.Containers[0].Ports[0].ContainerPort,
			)
		}
	}

	// A subsequent change to another port fails before any new replica listens. Both stable
	// endpoints must continue reaching the old replicas, now listening on 9090.
	checkpoint("before failed port change", false)
	service.Image = p.CrashingImage
	service.Port = new(int32(10000))
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	if result := w.Loco(t.Context(), dir, nil, "deploy", "--yes", "--wait"); result.Err == nil {
		t.Fatal("the non-listening version deployed successfully")
	}
	checkpoint("after failed port change", true)
	phase := regionStatus(t, w, app).GetDeploymentStatus()
	if phase != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		t.Errorf("deployment phase = %v, want failed", phase)
	}
	if !p.Reaches(
		t,
		platform.ProbePod{Namespace: w.Namespace, Name: "port-rollout-explicit"},
		"http://"+app+":10000/",
	) {
		t.Error("the newly configured private Service port does not reach the ready old replicas")
	}
	p.DeleteOnCleanup(t, "-n", w.Namespace, "pod", "port-rollout-explicit")
	checkpoint("after explicit private port probe", false)
}

type portTraffic struct{ success, failures int }

func startPortPoller(t *testing.T, namespace, name, url, host string) func() portTraffic {
	t.Helper()
	header := ""
	if host != "" {
		header = fmt.Sprintf("--header %q", "Host: "+host)
	}
	p.StartPod(t, platform.ProbePod{Namespace: namespace, Name: name, Command: []string{
		"sh", "-c", fmt.Sprintf(
			`while :; do
  output=$(wget -T 2 -O /dev/null %s %q 2>&1)
  status=$?
  if [ "$status" -eq 0 ]; then echo ok
  else echo fail; printf 'wget exit=%%s: %%s\n' "$status" "$output"; fi
  sleep 0.2
done`,
			header, url,
		),
	}})
	p.DeleteOnCleanup(t, "-n", namespace, "pod", name)
	// Cleanup runs in reverse order: capture the poller before DeleteOnCleanup removes it.
	t.Cleanup(func() {
		if t.Failed() {
			logs, err := p.Kubectl(context.WithoutCancel(t.Context()), "-n", namespace, "logs", name, "--timestamps")
			t.Logf("request history for %s:\n%s\n%v", name, logs, err)
		}
	})
	p.MustKubectl(t, "-n", namespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=60s")
	var started corev1.Pod
	p.MustGet(t, &started, "-n", namespace, "pod", name)
	traffic := func() portTraffic {
		var current corev1.Pod
		p.MustGet(t, &current, "-n", namespace, "pod", name)
		if current.UID != started.UID || current.Status.Phase != corev1.PodRunning ||
			len(current.Status.ContainerStatuses) != 1 || current.Status.ContainerStatuses[0].RestartCount != 0 {
			t.Fatalf("poller %s changed identity or stopped: uid=%s phase=%s statuses=%+v", name,
				current.UID, current.Status.Phase, current.Status.ContainerStatuses)
		}
		logs := p.MustKubectl(t, "-n", namespace, "logs", name)
		var result portTraffic
		for line := range strings.SplitSeq(logs, "\n") {
			switch line {
			case "ok":
				result.success++
			case "fail":
				result.failures++
			}
		}
		return result
	}
	platform.Eventually(t, 2*time.Minute, name+" to serve requests", func(context.Context) error {
		if served := traffic().success; served < 10 {
			return fmt.Errorf("%d requests served", served)
		}
		return nil
	})
	return traffic
}

// recordPortTransitions keeps only lifecycle/port fields, never Pod environment or Secret data.
func recordPortTransitions(t *testing.T, namespace, app, selector string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var watches sync.WaitGroup
	for _, query := range []struct{ resource, selector, projection string }{
		{"pods", selector, `{{.type}} pod={{.object.metadata.name}} uid={{.object.metadata.uid}} ` +
			`created={{.object.metadata.creationTimestamp}} deleting={{.object.metadata.deletionTimestamp}} ` +
			`{{range .object.spec.containers}}{{range .ports}}port={{.containerPort}} {{end}}{{end}}` +
			`{{range .object.status.conditions}}{{if eq .type "Ready"}}` +
			`ready={{.status}} changed={{.lastTransitionTime}} {{end}}{{end}}` +
			`{{range .object.status.containerStatuses}}{{if .state.terminated}}` +
			`exit={{.state.terminated.exitCode}} finished={{.state.terminated.finishedAt}}{{end}}{{end}}{{"\n"}}`},
		{"endpointslices", "kubernetes.io/service-name=" + app,
			`{{.type}} slice={{.object.metadata.name}} created={{.object.metadata.creationTimestamp}} ` +
				`{{range .object.metadata.managedFields}}updated={{.time}} {{end}}` +
				`{{range .object.ports}}{{.name}}={{.port}} {{end}}` +
				`{{range .object.endpoints}}pod={{.targetRef.name}} ready={{.conditions.ready}} ` +
				`serving={{.conditions.serving}} terminating={{.conditions.terminating}} {{end}}{{"\n"}}`},
	} {
		watches.Go(func() {
			started := time.Now().UTC()
			out, err := p.Kubectl(ctx, "-n", namespace, "get", query.resource, "-l", query.selector,
				"--watch", "--output-watch-events", "--show-managed-fields", "-o", "go-template="+query.projection)
			if ctx.Err() != context.Canceled {
				t.Errorf("%s transition watch stopped before cleanup: %v", query.resource, err)
			}
			if t.Failed() {
				t.Logf("%s to %s %s transitions:\n%s", started.Format(time.RFC3339Nano),
					time.Now().UTC().Format(time.RFC3339Nano), query.resource, out)
			}
		})
	}
	t.Cleanup(func() {
		cancel()
		watches.Wait()
	})
}
