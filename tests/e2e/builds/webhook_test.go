package builds

import (
	"context"
	"fmt"
	"testing"
	"time"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const webhookTimeout = 30 * time.Second

func TestFailedBuildReachesInstallWebhook(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const name = "e2e-webhook-build"
	created, err := p.User.Resource.CreateResource(t.Context(), &resourcev1.CreateResourceRequest{
		EnvironmentId: w.EnvironmentID,
		WorkspaceId:   w.ID,
		Name:          name,
		Type:          resourcev1.ResourceType_RESOURCE_TYPE_SERVICE,
		Spec: &resourcev1.ResourceSpec{Spec: &resourcev1.ResourceSpec_Service{Service: &resourcev1.ServiceSpec{
			Routing: &resourcev1.RoutingConfig{Port: new(int32(8080)), PathPrefix: "/"},
			Regions: map[string]*resourcev1.RegionTarget{
				p.Region: {Enabled: true, Cpu: "100m", Memory: "64Mi", MinReplicas: 1, MaxReplicas: 1},
			},
			HealthCheck: &deploymentv1.HealthCheckConfig{
				Path: "/healthz", IntervalSeconds: 5, TimeoutSeconds: 3, FailureThreshold: 3,
			},
		}}},
	})
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}

	started := p.StartFailingBuild(t, created.GetResourceId(), "broken")
	if finished := waitBuildFinished(t, started.ID); finished.GetStatus() != buildv1.BuildStatus_BUILD_STATUS_FAILED {
		t.Fatalf("build ended %v, want failed", finished.GetStatus())
	}

	const what = "the install webhook to receive the build failure"
	platform.Eventually(t, webhookTimeout, what, func(context.Context) error {
		for _, event := range p.Webhook.Events() {
			if event.Type == "build.failed" && event.Subject.ID == started.ID {
				return nil
			}
		}
		return fmt.Errorf("no build.failed event for build %s among %d", started.ID, len(p.Webhook.Events()))
	})
}
