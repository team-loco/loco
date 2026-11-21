package apps

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestCancelingAStuckDeployKeepsThePreviousPods cancels a version that never listens on its port.
// The previous version's pods keep running through the cancel and the region turns healthy again.
func TestCancelingAStuckDeployKeepsThePreviousPods(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-cancel-rollout"
	service := platform.ImageService(p.PublicImage, w.Host(app))
	service.Health = nil
	service.Env = map[string]string{versionModeKey: "first"}
	target := platform.OneReplica()
	target.Replicas = &locofile.Replicas{Min: new(int32(2)), Max: new(int32(2))}
	service.Regions = map[string]locofile.Region{p.Region: target}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	previous := regionStatus(t, w, app).GetDeploymentId()
	selector := "app=" + locov1alpha1.WorkloadName(w.ResourceID(t, app))
	serving := runningPods(appPods(t, w, selector), "first")
	if len(serving) != 2 {
		t.Fatalf("serving pods=%v, want 2", serving)
	}

	listening := withImage(service, p.AlternatePortImage)
	listening.Env = map[string]string{versionModeKey: "stuck"}
	file.Services[app] = listening
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	response, err := p.User.Deployment.ListDeployments(t.Context(),
		&deploymentv1.ListDeploymentsRequest{ResourceId: w.ResourceID(t, app)})
	if err != nil {
		t.Fatal(err)
	}
	stuck := response.GetDeployments()[0].GetId()
	if stuck == previous {
		t.Fatal("the deploy created no deployment")
	}
	platform.Eventually(t, statusTimeout, "the stuck version to start", func(context.Context) error {
		if len(runningPods(appPods(t, w, selector), "stuck")) == 0 {
			return errors.New("no pod of the stuck version")
		}
		return nil
	})
	if _, err = p.User.Deployment.CancelDeployment(t.Context(),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: stuck}); err != nil {
		t.Fatal(err)
	}

	platform.Eventually(t, statusTimeout, "the previous version to serve alone", func(context.Context) error {
		region := regionStatus(t, w, app)
		if region.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY ||
			region.GetDeploymentId() != previous {
			return fmt.Errorf("region %v on deployment %s", region.GetStatus(), region.GetDeploymentId())
		}
		pods := appPods(t, w, selector)
		if live := runningPods(pods, ""); len(live) != len(pods) {
			return fmt.Errorf("%d of %d pods still stopping", len(pods)-len(live), len(pods))
		}
		return nil
	})
	if after := runningPods(appPods(t, w, selector), ""); !maps.Equal(after, serving) {
		t.Fatalf("pods after the cancel=%v, want the previous version's %v", after, serving)
	}
}

// runningPods names the pods not being deleted, by UID, of the version mode, or of any when it is empty.
func runningPods(pods []corev1.Pod, mode string) map[types.UID]string {
	running := map[types.UID]string{}
	for _, pod := range pods {
		if pod.DeletionTimestamp == nil && (mode == "" || templateEnv(pod.Spec, versionModeKey) == mode) {
			running[pod.UID] = pod.Name
		}
	}
	return running
}
