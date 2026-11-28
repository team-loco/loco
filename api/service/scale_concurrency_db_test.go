package service

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestScaleBuildsOnTheReplacementThatCommitsWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	initial := imageService()
	initial.Env = map[string]string{"KEY": "old"}
	if _, err := createDeploymentWith(t, f, sameRegionSpec, initial); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, true)
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: movedDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
		nil,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{ActorType: genDb.DeploymentActorTypeUser, Method: genDb.DeploymentMethodWeb, Scopes: scopes},
	)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	type createResult struct {
		response *deploymentv1.CreateDeploymentResponse
		err      error
	}
	created := make(chan createResult, 1)
	go func() {
		service := imageService()
		testTarget(service).Cpu = new(biggerCPU)
		testTarget(service).Memory = new("128Mi")
		service.Env = map[string]string{"KEY": "new"}
		response, err := server.CreateDeployment(
			ctx,
			&deploymentv1.CreateDeploymentRequest{
				RequestId:     uuid.NewString(),
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Regions:       []string{testRegion},
				Spec: &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: service},
				},
			},
		)
		created <- createResult{response: response, err: err}
	}()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	scaled := make(chan error, 1)
	go func() { scaled <- scaleResource(t, f, 2) }()
	dbtest.WaitForLockWaiters(t, f.pool, 2)
	release()
	replacement := <-created
	if replacement.err != nil {
		t.Fatal(replacement.err)
	}
	if err := <-scaled; err != nil {
		t.Fatal(err)
	}
	f.wantSuperseded(t, uuid.MustParse(replacement.response.GetDeploymentId()))
	desired := desiredPayload(t, f).AppSpec.ServiceSpec
	if desired.Deployment.Image != replacement.response.GetBuild().GetImage() || desired.Resources.CPU != biggerCPU ||
		desired.Resources.Memory != "128Mi" {
		t.Fatalf(
			"scaling dropped the replacement: image=%s cpu=%s memory=%s",
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
