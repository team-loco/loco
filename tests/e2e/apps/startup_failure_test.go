package apps

import (
	"context"
	"fmt"
	"testing"
	"time"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestInvalidContainerConfigFailsBeforeDeployTimeout(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-startup-failure"
	service := platform.ImageService(p.PublicImage, w.Host(app))
	// Keep this revision unproven until the configuration failure is injected.
	service.Port = new(int32(1))
	service.Health = nil
	service.Deploy = &locofile.Deploy{Restart: locofile.RestartAlways}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	resourceID := w.ResourceID(t, app)
	deploymentID := regionStatus(t, w, app).GetDeploymentId()
	// A missing environment Secret makes kubelet reject container configuration.
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{
		"name":%q,
		"env":[{"name":"STARTUP_FAILURE","valueFrom":{
			"secretKeyRef":{"name":"missing-startup-env","key":"value"}
		}}]
	}]}}}}`, loco.WorkloadName(resourceID))
	platform.Eventually(t, time.Minute, "workload to accept the missing-secret reference",
		func(ctx context.Context) error {
			_, err := p.Kubectl(ctx, "-n", w.Namespace, "patch", "deployment/"+loco.WorkloadName(resourceID),
				"--type=strategic", "-p", patch)
			return err
		})
	// The default deploy timeout is ten minutes. A two-minute bound rejects timeout-only failure.
	platform.Eventually(t, 2*time.Minute, "startup failure to fail the deployment", func(ctx context.Context) error {
		response, getErr := p.User.Deployment.GetDeployment(
			ctx,
			&deploymentv1.GetDeploymentRequest{DeploymentId: deploymentID},
		)
		if getErr != nil {
			return getErr
		}
		if response.GetDeployment().GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
			return fmt.Errorf("deployment status = %v", response.GetDeployment().GetStatus())
		}
		return nil
	})
	application, err := p.Application(t.Context(), resourceID, w.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	stopped := application.Status.Rollout.Stopped
	if stopped == nil {
		t.Fatal("no startup stop")
	}
	if stopped.Reason != loco.ProblemConfigError || stopped.Restarts != 0 {
		t.Fatalf("stopped = %+v, want config_error with zero restarts", stopped)
	}
}
