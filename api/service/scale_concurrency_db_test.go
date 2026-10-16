package service

import (
	"context"
	"sync"
	"testing"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestScaleUsesTheReplacementThatCommitsWhileItWaits(t *testing.T) {
	f := newDeployFixture(t)
	initial := imageService()
	initial.Env = map[string]string{"KEY": "old"}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, initial); err != nil {
		t.Fatal(err)
	}
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: movedDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	type createResult struct {
		response *deploymentv1.CreateDeploymentResponse
		err      error
	}
	created := make(chan createResult, 1)
	go func() {
		service := imageService()
		service.Cpu = new(biggerCPU)
		service.Memory = new("128Mi")
		service.Env = map[string]string{"KEY": "new"}
		response, err := server.CreateDeployment(
			ctx,
			&deploymentv1.CreateDeploymentRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Region:        testRegion,
				Spec: &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: service},
				},
			},
		)
		created <- createResult{response: response, err: err}
	}()
	f.waitForLockWaiters(t, 1)
	scaled := make(chan error, 1)
	go func() { scaled <- scaleResource(t, f, 2) }()
	f.waitForLockWaiters(t, 2)
	release()
	replacement := <-created
	if replacement.err != nil {
		t.Fatal(replacement.err)
	}
	if err := <-scaled; err != nil {
		t.Fatal(err)
	}
	desired := desiredPayload(t, f).AppSpec.ServiceSpec
	if desired.Deployment.Image != replacement.response.GetBuild().GetImage() || desired.Resources.CPU != biggerCPU ||
		desired.Resources.Memory != "128Mi" {
		t.Fatalf(
			"scaling restored stale deployment: image=%s cpu=%s memory=%s",
			desired.Deployment.Image,
			desired.Resources.CPU,
			desired.Resources.Memory,
		)
	}
	if desired.Deployment.Env["KEY"] != "new" {
		t.Fatalf("scaled env KEY = %q, want latest new value", desired.Deployment.Env["KEY"])
	}
	if desired.Resources.Replicas.Min != 2 || desired.Resources.Replicas.Max != 2 {
		t.Fatalf("scaled replicas = %+v, want 2-2", desired.Resources.Replicas)
	}
}
