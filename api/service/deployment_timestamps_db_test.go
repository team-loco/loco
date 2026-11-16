package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestDeploymentTimestampsFollowClusterReports(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id, err := f.deploy(t.Context(), staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	read := func() *deploymentv1.Deployment {
		t.Helper()
		row, readErr := f.queries.GetDeploymentByID(t.Context(), id)
		if readErr != nil {
			t.Fatal(readErr)
		}
		deployment, convertErr := deploymentToProto(row, genDb.ResourceTypeService, "", "")
		if convertErr != nil {
			t.Fatal(convertErr)
		}
		return deployment
	}
	pending := read()
	if pending.GetStartedAt() != nil || pending.GetCompletedAt() != nil {
		t.Fatalf("pending timestamps = %v, %v", pending.GetStartedAt(), pending.GetCompletedAt())
	}
	placement := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId: placement.ID.String(), ObservedRevision: placement.DesiredRevision,
		Phase: locov1alpha1.ApplicationPhaseDeploying,
	}
	server := f.agentServer()
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	started := read()
	if started.GetStartedAt() == nil || started.GetStartedAt().AsTime().Before(pending.GetCreatedAt().AsTime()) {
		t.Fatalf("deploying start = %v, created = %v", started.GetStartedAt(), pending.GetCreatedAt())
	}
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	if got := read().GetStartedAt(); !got.AsTime().Equal(started.GetStartedAt().AsTime()) {
		t.Fatalf("repeated report start = %v, want %v", got, started.GetStartedAt())
	}
	report.Phase = locov1alpha1.ApplicationPhaseFailed
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	finished := read()
	if finished.GetCompletedAt() == nil || !finished.GetStartedAt().AsTime().Equal(started.GetStartedAt().AsTime()) {
		t.Fatalf("failed timestamps = %v, %v", finished.GetStartedAt(), finished.GetCompletedAt())
	}
	server.recordStatus(t.Context(), f.clusterID, 0, report)
	if got := read(); !got.GetStartedAt().AsTime().Equal(started.GetStartedAt().AsTime()) ||
		!got.GetCompletedAt().AsTime().Equal(finished.GetCompletedAt().AsTime()) {
		t.Fatalf("repeated failure timestamps = %v, %v", got.GetStartedAt(), got.GetCompletedAt())
	}
}
