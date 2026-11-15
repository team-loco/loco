package apps

import (
	"context"
	"fmt"
	"strings"
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
	host := platform.Host(app)
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
	if p.Reaches(t, platform.ProbePod{Namespace: p.Namespace, Name: "port-rollout-denied"}, serviceURL(w, id)) {
		t.Fatal("a pod outside the workspace bypasses gateway ingress isolation")
	}
	p.DeleteOnCleanup(t, "-n", p.Namespace, "pod", "port-rollout-denied")
	gateway, err := p.GatewayService(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	public := startPortPoller(t, p.Namespace, "port-rollout-public",
		fmt.Sprintf("http://%s.%s.svc.cluster.local/", gateway, p.Namespace), host)
	private := startPortPoller(t, w.Namespace, "port-rollout-private", "http://"+app+"/", "")
	before := []int{public("ok"), private("ok")}
	failuresBefore := []int{public("fail"), private("fail")}

	service.Image = p.AlternatePortImage
	service.Port = new(int32(9090))
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
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
	service.Image = p.CrashingImage
	service.Port = new(int32(10000))
	file.Services[app] = service
	platform.WriteLocoFile(t, dir, file)
	if result := w.Loco(t.Context(), dir, nil, "deploy", "--yes", "--wait"); result.Err == nil {
		t.Fatal("the non-listening version deployed successfully")
	}
	phase := regionStatus(t, w, app).GetDeploymentStatus()
	if phase != deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED {
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
	for i, poller := range []func(string) int{public, private} {
		if served := poller("ok"); served <= before[i] {
			t.Errorf("request stream %d stopped serving (%d before, %d after)", i, before[i], served)
		}
		if failed := poller("fail") - failuresBefore[i]; failed != 0 {
			t.Errorf("request stream %d failed %d requests across port changes", i, failed)
		}
	}
}

func startPortPoller(t *testing.T, namespace, name, url, host string) func(string) int {
	t.Helper()
	header := ""
	if host != "" {
		header = fmt.Sprintf("--header %q", "Host: "+host)
	}
	p.StartPod(t, platform.ProbePod{Namespace: namespace, Name: name, Command: []string{
		"sh", "-c", fmt.Sprintf(
			`while :; do if wget -q -T 2 -O /dev/null %s %q; then echo ok; else echo fail; fi; sleep 0.2; done`,
			header, url,
		),
	}})
	p.DeleteOnCleanup(t, "-n", namespace, "pod", name)
	p.MustKubectl(t, "-n", namespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=60s")
	count := func(result string) int {
		logs := p.MustKubectl(t, "-n", namespace, "logs", name)
		return strings.Count("\n"+logs, "\n"+result+"\n")
	}
	platform.Eventually(t, 2*time.Minute, name+" to serve requests", func(context.Context) error {
		if served := count("ok"); served < 10 {
			return fmt.Errorf("%d requests served", served)
		}
		return nil
	})
	return count
}
