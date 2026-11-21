package apps

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const cancelSecretKey = "CANCEL_SECRET"

// TestCancelingAStuckDeployKeepsThePreviousPods cancels a version that never listens on its port.
// The previous version's pods keep running through the cancel and the region turns healthy again.
func TestCancelingAStuckDeployKeepsThePreviousPods(t *testing.T) {
	t.Parallel()
	c := deployThenStick(t, "e2e-cancel-rollout")
	c.cancel(t)
	if after := c.runningAfterCancel(t); !maps.Equal(after, c.serving) {
		t.Fatalf("pods after the cancel=%v, want the previous version's %v", after, c.serving)
	}
}

// TestCancelingAfterASecretChangeRollsThePreviousPods changes a secret the previous version reads
// before the cancel: its pods read secrets only when they start, so the cancel replaces them.
func TestCancelingAfterASecretChangeRollsThePreviousPods(t *testing.T) {
	t.Parallel()
	c := deployThenStick(t, "e2e-cancel-secret")
	c.w.MustLoco(t, c.dir, "secret", "set", "--env", platform.Production, cancelSecretKey+"=second")
	c.cancel(t)
	for uid, name := range c.runningAfterCancel(t) {
		if _, kept := c.serving[uid]; kept {
			t.Fatalf("pod %s kept running with the previous secret value", name)
		}
	}
	out := p.MustKubectl(t, "-n", c.w.Namespace, "exec", "deployment/"+c.workload, "--", "printenv", cancelSecretKey)
	if got := strings.TrimSpace(out); got != "second" {
		t.Fatalf("%s after the cancel = %q, want second", cancelSecretKey, got)
	}
}

type stuckDeploy struct {
	w        *platform.Workspace
	dir      string
	app      string
	workload string
	previous string
	stuck    string
	// serving holds the previous version's pods by UID.
	serving map[types.UID]string
}

// deployThenStick deploys two replicas that read a secret, then a version listening on the wrong
// port, and waits for that version's first pod.
func deployThenStick(t *testing.T, app string) *stuckDeploy {
	t.Helper()
	w := p.NewTenant(t)
	dir := t.TempDir()
	w.MustLoco(t, dir, "secret", "set", "--env", platform.Production, cancelSecretKey+"=first")
	service := platform.ImageService(p.PublicImage, w.Host(app))
	service.Health = nil
	service.Env = map[string]string{versionModeKey: "first", cancelSecretKey: "${{ secrets." + cancelSecretKey + " }}"}
	target := platform.OneReplica()
	target.Replicas = &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))}
	service.Regions = map[string]locofile.Region{p.Region: target}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	c := &stuckDeploy{
		w: w, dir: dir, app: app, workload: locov1alpha1.WorkloadName(w.ResourceID(t, app)),
		previous: regionStatus(t, w, app).GetDeploymentId(),
	}
	c.serving = runningPods(appPods(t, w, c.selector()), "first")
	if len(c.serving) != 2 {
		t.Fatalf("serving pods=%v, want 2", c.serving)
	}

	listening := withImage(service, p.AlternatePortImage)
	listening.Env = maps.Clone(service.Env)
	listening.Env[versionModeKey] = "stuck"
	file.Services[app] = listening
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	response, err := p.User.Deployment.ListDeployments(t.Context(),
		&deploymentv1.ListDeploymentsRequest{ResourceId: w.ResourceID(t, app)})
	if err != nil {
		t.Fatal(err)
	}
	c.stuck = response.GetDeployments()[0].GetId()
	if c.stuck == c.previous {
		t.Fatal("the deploy created no deployment")
	}
	platform.Eventually(t, statusTimeout, "the stuck version to start", func(context.Context) error {
		if len(runningPods(appPods(t, w, c.selector()), "stuck")) == 0 {
			return errors.New("no pod of the stuck version")
		}
		return nil
	})
	return c
}

func (c *stuckDeploy) selector() string { return "app=" + c.workload }

func (c *stuckDeploy) cancel(t *testing.T) {
	t.Helper()
	if _, err := p.User.Deployment.CancelDeployment(t.Context(),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: c.stuck}); err != nil {
		t.Fatal(err)
	}
}

// runningAfterCancel waits for the region to serve the previous deployment alone and returns its pods.
func (c *stuckDeploy) runningAfterCancel(t *testing.T) map[types.UID]string {
	t.Helper()
	platform.Eventually(t, statusTimeout, "the previous version to serve alone", func(context.Context) error {
		region := regionStatus(t, c.w, c.app)
		if region.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY ||
			region.GetDeploymentId() != c.previous {
			return fmt.Errorf("region %v on deployment %s", region.GetStatus(), region.GetDeploymentId())
		}
		pods := appPods(t, c.w, c.selector())
		if live := runningPods(pods, "first"); len(live) != len(pods) {
			return fmt.Errorf("%d of %d pods still stopping or stuck", len(pods)-len(live), len(pods))
		}
		return nil
	})
	return runningPods(appPods(t, c.w, c.selector()), "first")
}

// runningPods names the pods not being deleted, by UID, of the version mode.
func runningPods(pods []corev1.Pod, mode string) map[types.UID]string {
	running := map[types.UID]string{}
	for _, pod := range pods {
		if pod.DeletionTimestamp == nil && templateEnv(pod.Spec, versionModeKey) == mode {
			running[pod.UID] = pod.Name
		}
	}
	return running
}
