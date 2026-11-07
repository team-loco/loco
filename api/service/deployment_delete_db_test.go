package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func (f *deployFixture) deploymentWriter(t *testing.T) (*DeploymentServer, context.Context) {
	t.Helper()
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{digest: testDigest},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
	)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	return server, reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
}

func TestDeletingRunningDeploymentFinalizesItsStatus(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	deploymentID := uuid.MustParse(created.GetDeploymentId())
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("status before delete = %s, want running", status)
	}
	server, ctx := f.deploymentWriter(t)
	_, err = server.DeleteDeployment(ctx, &deploymentv1.DeleteDeploymentRequest{DeploymentId: deploymentID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusSucceeded {
		t.Fatalf("status after delete = %s, want succeeded", status)
	}
}

func TestDeletingSupersededDeploymentPreservesConcurrentReplacement(t *testing.T) {
	f := newDeployFixture(t)
	original, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	server, ctx := f.deploymentWriter(t)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	type createResult struct {
		response *deploymentv1.CreateDeploymentResponse
		err      error
	}
	created := make(chan createResult, 1)
	go func() {
		service := imageService()
		response, createErr := server.CreateDeployment(
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
		created <- createResult{response: response, err: createErr}
	}()
	f.waitForLockWaiters(t, 1)
	deleted := make(chan error, 1)
	go func() {
		_, deleteErr := server.DeleteDeployment(
			ctx,
			&deploymentv1.DeleteDeploymentRequest{DeploymentId: original.GetDeploymentId()},
		)
		deleted <- deleteErr
	}()
	f.waitForLockWaiters(t, 2)
	release()
	replacement := <-created
	if replacement.err != nil {
		t.Fatal(replacement.err)
	}
	if err = <-deleted; err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	if placement.DesiredDeleted || placement.DeploymentID == nil ||
		placement.DeploymentID.String() != replacement.response.GetDeploymentId() {
		t.Fatalf(
			"deleting superseded deployment removed replacement placement: deleted=%v deployment=%v; want %s",
			placement.DesiredDeleted,
			placement.DeploymentID,
			replacement.response.GetDeploymentId(),
		)
	}
	_, err = server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: replacement.response.GetDeploymentId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !f.placement(t, f.clusterID).DesiredDeleted {
		t.Fatal("deleting the current deployment did not mark its placement deleted")
	}
}
