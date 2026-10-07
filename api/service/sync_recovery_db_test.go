package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestReadyAtTheCurrentRevisionRecoversAFailedDeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	deploymentID, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, applied(p.ID, 1))

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Phase: applicationPhaseFailed, Message: "pull secret",
	})
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status after a ready report = %s, want running", status)
	}
	d, err := f.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.CompletedAt != nil {
		t.Fatal("a running deployment kept its completed_at")
	}
}

func TestReadyAtAnOlderRevisionDoesNotRecoverAFailedDeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	second, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, &agentv1.Applied{
		PlacementId: p.ID.String(), Revision: 2, Error: "invalid spec",
	})
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusFailed {
		t.Fatalf("a ready report for the previous revision moved the deployment to %s", status)
	}
}

func TestInventoryAheadOfTheDatabaseAdvancesThePlacement(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)

	var sent []*agentv1.SyncResponse
	session := newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})

	err := session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: p.ID.String(), Revision: 5},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := f.placement(t, f.clusterID); got.DesiredRevision != 6 {
		t.Fatalf("desired revision = %d, want 6", got.DesiredRevision)
	}
	if len(sent) != 1 || sent[0].GetApply().GetRevision() != 6 {
		t.Fatalf("sent %v, want one Apply at revision 6", sent)
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 6))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 6 {
		t.Fatalf("applied revision = %d, want 6", got.AppliedRevision)
	}

	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return removePlacement(ctx, qtx, f.resourceID, f.clusterID)
	})
	if err != nil {
		t.Fatalf("remove placement: %v", err)
	}
	sent = nil
	session = newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	err = session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: p.ID.String(), Revision: 9},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	tombstone := f.placement(t, f.clusterID)
	if !tombstone.DesiredDeleted || tombstone.DesiredRevision != 10 {
		t.Fatalf(
			"tombstone = deleted %v rev %d, want deleted at 10",
			tombstone.DesiredDeleted,
			tombstone.DesiredRevision,
		)
	}
	if len(sent) != 1 || sent[0].GetDelete().GetRevision() != 10 {
		t.Fatalf("sent %v, want one Delete at revision 10", sent)
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 10))
	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 0 {
		t.Fatal("tombstone not removed after the advanced delete was acked")
	}
}

func TestRedeployReadsEnvInsideItsTransaction(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()

	envSpec := func(env map[string]string) desiredSpecFunc {
		return func(_ uuid.UUID) ([]byte, error) {
			return json.Marshal(ApplicationPayload{AppSpec: &locoControllerV1.ApplicationSpec{
				ServiceSpec: &locoControllerV1.ServiceSpec{
					Deployment: &locoControllerV1.ServiceDeploymentSpec{Env: env},
				},
			}})
		}
	}
	if _, err := f.deploy(ctx, envSpec(map[string]string{"KEY": "old"})); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	clusterID := f.clusterID
	service := &deploymentv1.ServiceDeploymentSpec{}
	plan := regionRedeploy{
		params:           f.paramsFor(f.clusterID),
		deploymentSpec:   &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{Service: service}},
		envSourceCluster: &clusterID,
	}

	if _, err := f.deploy(ctx, envSpec(map[string]string{"KEY": "new"})); err != nil {
		t.Fatalf("env update deploy: %v", err)
	}

	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return inheritDesiredEnv(ctx, qtx, plan, f.cipher)
	})
	if err != nil {
		t.Fatalf("inherit env: %v", err)
	}
	if got := plan.deploymentSpec.GetService().GetEnv()["KEY"]; got != "new" {
		t.Fatalf("redeploy env KEY = %q, want the value committed before its transaction", got)
	}
}
