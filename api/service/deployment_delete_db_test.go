package service

import (
	"context"
	"sync"
	"testing"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestDeletingSupersededDeploymentPreservesConcurrentReplacement(t *testing.T) {
	f := newDeployFixture(t)
	original, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
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
