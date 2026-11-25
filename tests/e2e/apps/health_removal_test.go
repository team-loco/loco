package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// TestRemoveHealthCheck removes a service's HTTP health check through CreateDeployment: the
// rollout's pods probe only the port, and a later deployment without a check does not bring the
// HTTP check back.
func TestRemoveHealthCheck(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-health-off"
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{
		Partial:  app,
		Services: map[string]locofile.Service{app: platform.ImageService(p.PublicImage)},
	})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	id := w.ResourceID(t, app)
	selector := "app=resource-" + id
	waitProbes(t, w, selector, true)

	for _, remove := range []bool{true, false} {
		created, err := p.User.Deployment.CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{
			ResourceId:    id,
			EnvironmentId: w.EnvironmentID,
			Regions:       []string{p.Region},
			Spec: &deploymentv1.DeploymentSpec{
				Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
					Build: &deploymentv1.BuildSource{Type: imageBuild, Image: p.PublicImage},
					Port:  new(int32(8080)),
				}},
			},
			RemoveHealthCheck: remove,
		})
		if err != nil {
			t.Fatalf("create deployment (remove_health_check %v): %v", remove, err)
		}
		waitServing(t, w, app, created.GetDeploymentId())
		waitProbes(t, w, selector, false)
	}
}

// waitServing waits until the region runs the deployment and the resource is healthy.
func waitServing(t *testing.T, w *platform.Workspace, app, deploymentID string) {
	t.Helper()
	platform.Eventually(t, statusTimeout, app+" to run "+deploymentID, func(ctx context.Context) error {
		current, err := statusOf(ctx, w, app)
		if err != nil {
			return err
		}
		if current.region.GetDeploymentId() != deploymentID {
			return fmt.Errorf("region runs %s", current.region.GetDeploymentId())
		}
		if current.status != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY {
			return fmt.Errorf("status %v", current.status)
		}
		return nil
	})
}

// waitProbes waits until every pod of the app probes HTTP when httpCheck is set, and only the
// port otherwise.
func waitProbes(t *testing.T, w *platform.Workspace, selector string, httpCheck bool) {
	t.Helper()
	platform.Eventually(t, statusTimeout, fmt.Sprintf("pods with an HTTP check %v", httpCheck),
		func(context.Context) error {
			pods := appPods(t, w, selector)
			if len(pods) == 0 {
				return errors.New("no pods")
			}
			for _, pod := range pods {
				readiness := pod.Spec.Containers[0].ReadinessProbe
				if readiness == nil {
					return fmt.Errorf("pod %s has no readiness probe", pod.Name)
				}
				if (readiness.HTTPGet != nil) != httpCheck {
					return fmt.Errorf("pod %s readiness probe %+v", pod.Name, readiness.ProbeHandler)
				}
			}
			return nil
		})
}
