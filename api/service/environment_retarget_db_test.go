package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestStopThenRetargetReusesUnappliedPlacement(t *testing.T) {
	f := newDeployFixture(t)
	qaID := f.addTypedEnvironment(t, "qa", "production")
	original, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	initial := f.placement(t, f.clusterID)
	f.agentServer().recordApplied(t.Context(), f.clusterID, applied(initial.ID, initial.DesiredRevision))
	f.finishRollouts(t)
	before := f.placement(t, f.clusterID)
	if before.AppliedRevision != before.DesiredRevision {
		t.Fatal("initial deployment was not acknowledged")
	}
	server := NewDeploymentServer(f.pool, f.queries, &fakeResolver{digest: testDigest},
		testRegistryHost, testServiceDefaults(), testClusterSilentAfter)
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	_, err = server.DeleteDeployment(
		ctx,
		&deploymentv1.DeleteDeploymentRequest{DeploymentId: original.GetDeploymentId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	stopped := f.placement(t, f.clusterID)
	if !stopped.DesiredDeleted || stopped.AppliedRevision != before.AppliedRevision {
		t.Fatalf("stop did not retain unapplied deletion: %+v", stopped)
	}
	service := imageService()
	retargeted, err := server.CreateDeployment(ctx, &deploymentv1.CreateDeploymentRequest{
		ResourceId: f.resourceID.String(), EnvironmentId: qaID.String(), Region: testRegion,
		Spec: &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
	})
	if err != nil {
		t.Fatalf("retarget after stop: %v", err)
	}
	after := f.placement(t, f.clusterID)
	if after.ID != before.ID || after.EnvironmentID != qaID || after.DesiredDeleted ||
		after.AppliedRevision != before.AppliedRevision || after.DesiredRevision <= stopped.DesiredRevision ||
		after.DeploymentID == nil || after.DeploymentID.String() != retargeted.GetDeploymentId() {
		t.Fatalf("retarget did not replace the pending deletion in the same placement: %+v", after)
	}
	var payload ApplicationPayload
	if err = json.Unmarshal(after.DesiredSpec, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AppSpec.EnvironmentID != qaID.String() {
		t.Fatalf("payload environment = %s, want %s", payload.AppSpec.EnvironmentID, qaID)
	}
}
