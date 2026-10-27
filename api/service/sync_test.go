package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const testPhaseReady = "Ready"

func TestDiffInventory(t *testing.T) {
	cases := []struct {
		name string
		rev  placementRevision
		want inventoryAction
	}{
		{
			name: "missing from the cluster",
			rev:  placementRevision{desiredRevision: 3},
			want: inventorySend,
		},
		{
			name: "older revision in the cluster",
			rev:  placementRevision{desiredRevision: 3, observed: true, observedRevision: 2},
			want: inventorySend,
		},
		{
			name: "current revision, ack lost",
			rev:  placementRevision{desiredRevision: 3, observed: true, observedRevision: 3, appliedRevision: 2},
			want: inventoryMarkApplied,
		},
		{
			name: "current revision, already acked",
			rev:  placementRevision{desiredRevision: 3, observed: true, observedRevision: 3, appliedRevision: 3},
			want: inventoryInSync,
		},
		{
			name: "current revision with the env Secret pending",
			rev: placementRevision{
				desiredRevision: 3, observed: true, observedRevision: 3, appliedRevision: 2, envSecretPending: true,
			},
			want: inventorySend,
		},
		{
			name: "acked revision whose env Secret is pending again",
			rev: placementRevision{
				desiredRevision: 3, observed: true, observedRevision: 3, appliedRevision: 3, envSecretPending: true,
			},
			want: inventorySend,
		},
		{
			name: "newer pending revision in the cluster",
			rev:  placementRevision{desiredRevision: 1, observed: true, observedRevision: 5, envSecretPending: true},
			want: inventoryAhead,
		},
		{
			name: "newer revision in the cluster",
			rev:  placementRevision{desiredRevision: 3, observed: true, observedRevision: 4},
			want: inventoryAhead,
		},
		{
			name: "deleted and still present",
			rev:  placementRevision{desiredRevision: 4, desiredDeleted: true, observed: true, observedRevision: 3},
			want: inventorySend,
		},
		{
			name: "deleted, cluster holds a newer revision",
			rev:  placementRevision{desiredRevision: 4, desiredDeleted: true, observed: true, observedRevision: 6},
			want: inventoryAhead,
		},
		{
			name: "deleted and absent",
			rev:  placementRevision{desiredRevision: 4, desiredDeleted: true},
			want: inventorySend,
		},
		{
			name: "suspended and still present at the current revision",
			rev: placementRevision{
				desiredRevision: 4, suspended: true, observed: true, observedRevision: 4, appliedRevision: 4,
			},
			want: inventorySend,
		},
		{
			name: "suspended and still present at an older revision",
			rev:  placementRevision{desiredRevision: 4, suspended: true, observed: true, observedRevision: 3},
			want: inventorySend,
		},
		{
			name: "suspended, absent, stop not yet acked",
			rev:  placementRevision{desiredRevision: 4, suspended: true, appliedRevision: 3},
			want: inventorySend,
		},
		{
			name: "suspended, absent, stop acked",
			rev:  placementRevision{desiredRevision: 4, suspended: true, appliedRevision: 4},
			want: inventoryInSync,
		},
		{
			name: "suspended, cluster holds a newer revision",
			rev:  placementRevision{desiredRevision: 4, suspended: true, observed: true, observedRevision: 6},
			want: inventoryAhead,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := diffInventory(tc.rev); got != tc.want {
				t.Fatalf("diffInventory = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlacementMessage(t *testing.T) {
	placement := genDb.Placement{
		ID:              uuid.New(),
		ResourceID:      uuid.New(),
		DesiredRevision: 7,
		DesiredSpec:     []byte(`{"resource_id":"x"}`),
	}

	apply := placementMessage(placement, nil).GetApply()
	if apply == nil {
		t.Fatal("live placement did not produce an Apply")
	}
	if apply.GetPlacementId() != placement.ID.String() || apply.GetRevision() != 7 ||
		apply.GetResourceId() != placement.ResourceID.String() || string(apply.GetApplication()) != `{"resource_id":"x"}` {
		t.Fatalf("apply = %v", apply)
	}

	placement.DesiredDeleted = true
	placement.DesiredSpec = nil
	del := placementMessage(placement, nil).GetDelete()
	if del == nil {
		t.Fatal("deleted placement did not produce a Delete")
	}
	if del.GetPlacementId() != placement.ID.String() || del.GetRevision() != 7 ||
		del.GetResourceId() != placement.ResourceID.String() {
		t.Fatalf("delete = %v", del)
	}
}

func TestDeploymentTransitionForStatus(t *testing.T) {
	cases := []struct {
		name   string
		status *agentv1.PlacementStatus
		want   genDb.DeploymentStatus
		from   []genDb.DeploymentStatus
	}{
		{
			name:   "failed",
			status: &agentv1.PlacementStatus{Phase: applicationPhaseFailed},
			want:   genDb.DeploymentStatusFailed,
			from: []genDb.DeploymentStatus{
				genDb.DeploymentStatusPending,
				genDb.DeploymentStatusDeploying,
			},
		},
		{
			name:   "ready",
			status: &agentv1.PlacementStatus{Phase: testPhaseReady, Ready: true},
			want:   genDb.DeploymentStatusRunning,
			from: []genDb.DeploymentStatus{
				genDb.DeploymentStatusPending,
				genDb.DeploymentStatusDeploying,
				genDb.DeploymentStatusFailed,
			},
		},
		{
			name:   "rolling out",
			status: &agentv1.PlacementStatus{Phase: "Deploying"},
			want:   genDb.DeploymentStatusDeploying,
			from: []genDb.DeploymentStatus{
				genDb.DeploymentStatusPending,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transition := deploymentTransitionForStatus(tc.status)
			if transition.status != tc.want {
				t.Fatalf("status = %s, want %s", transition.status, tc.want)
			}
			if !slices.Equal(transition.from, tc.from) {
				t.Fatalf("from = %v, want %v", transition.from, tc.from)
			}
		})
	}
}
