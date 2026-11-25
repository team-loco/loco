package apps

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/internal/locofile"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestInvalidContainerConfigFailsBeforeDeployTimeout(t *testing.T) {
	t.Parallel()
	// Reuse the pinned fixture's layers with a root USER, which runAsNonRoot rejects.
	source, err := name.ParseReference(p.PublicImage, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	image, err := remote.Image(source, remote.WithContext(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	config, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config = config.DeepCopy()
	config.Config.User = "0"
	image, err = mutate.ConfigFile(image, config)
	if err != nil {
		t.Fatal(err)
	}
	target := source.Context().Tag("e2e-root-user")
	if err = remote.Write(target, image, remote.WithContext(t.Context())); err != nil {
		t.Fatal(err)
	}

	w := p.NewTenant(t)
	const app = "e2e-startup-failure"
	service := platform.ImageService(target.Name(), w.Host(app))
	service.Health = nil
	service.Deploy = &locofile.Deploy{Restart: locofile.RestartAlways}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "deploy", "--yes")
	resourceID := w.ResourceID(t, app)
	deploymentID := regionStatus(t, w, app).GetDeploymentId()
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
	if stopped.Reason != loco.ProblemImageUser || stopped.Restarts != 0 {
		t.Fatalf("stopped = %+v, want image_user with zero restarts", stopped)
	}
}
