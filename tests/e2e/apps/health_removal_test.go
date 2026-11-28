package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

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
		created, err := p.User.Deployment.CreateDeployment(
			t.Context(),
			&deploymentv1.CreateDeploymentRequest{
				RequestId:     uuid.NewString(),
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
			},
		)
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

// waitProbes waits until every pod of the app probes HTTP when httpCheck is set, and otherwise
// only checks that port 8080 accepts connections.
func waitProbes(t *testing.T, w *platform.Workspace, selector string, httpCheck bool) {
	t.Helper()
	platform.Eventually(t, statusTimeout, fmt.Sprintf("pods with an HTTP check %v", httpCheck),
		func(ctx context.Context) error {
			var pods corev1.PodList
			if err := p.Get(ctx, &pods, "-n", w.Namespace, "pods", "-l", selector); err != nil {
				return err
			}
			if len(pods.Items) == 0 {
				return errors.New("no pods")
			}
			for _, pod := range pods.Items {
				if !probesAsWanted(pod.Spec.Containers[0].ReadinessProbe, httpCheck) {
					return fmt.Errorf("pod %s readiness probe %+v", pod.Name, pod.Spec.Containers[0].ReadinessProbe)
				}
			}
			return nil
		})
}

func probesAsWanted(readiness *corev1.Probe, httpCheck bool) bool {
	if readiness == nil {
		return false
	}
	if httpCheck {
		return readiness.HTTPGet != nil
	}
	return readiness.TCPSocket != nil && readiness.TCPSocket.Port.IntValue() == 8080
}
