package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
)

const (
	testSuspensionReason  = "unpaid invoice"
	otherSuspensionReason = "legal hold"
)

func (f *deployFixture) suspend(t *testing.T, reason string) (*orgv1.SuspendOrgResponse, error) {
	t.Helper()
	server := NewOrgServer(f.pool, f.queries, nil)
	req := &orgv1.SuspendOrgRequest{OrgId: f.organizationID(t), Reason: reason}
	return server.SuspendOrg(systemAdminContext(t), req)
}

func (f *deployFixture) resume(t *testing.T) (*orgv1.ResumeOrgResponse, error) {
	t.Helper()
	server := NewOrgServer(f.pool, f.queries, nil)
	return server.ResumeOrg(systemAdminContext(t), &orgv1.ResumeOrgRequest{OrgId: f.organizationID(t)})
}

func (f *deployFixture) mustSuspend(t *testing.T) *orgv1.Organization {
	t.Helper()
	resp, err := f.suspend(t, testSuspensionReason)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	return resp.GetOrganization()
}

func (f *deployFixture) mustResume(t *testing.T) *orgv1.Organization {
	t.Helper()
	resp, err := f.resume(t)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	return resp.GetOrganization()
}

func (f *deployFixture) orgEvents(t *testing.T, eventType string) []genDb.Event {
	t.Helper()
	rows, err := f.pool.Query(
		t.Context(),
		`SELECT data FROM events WHERE type = $1 AND org_id = $2 AND subject_type = $3 ORDER BY seq`,
		eventType,
		f.organizationID(t),
		events.SubjectOrg,
	)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	defer rows.Close()
	var found []genDb.Event
	for rows.Next() {
		var event genDb.Event
		if scanErr := rows.Scan(&event.Data); scanErr != nil {
			t.Fatalf("scan event: %v", scanErr)
		}
		found = append(found, event)
	}
	if rows.Err() != nil {
		t.Fatalf("list events: %v", rows.Err())
	}
	return found
}

func (f *deployFixture) pendingMessages(t *testing.T) []*agentv1.SyncResponse {
	t.Helper()
	var sent []*agentv1.SyncResponse
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	if err := session.sendPendingPlacements(t.Context()); err != nil {
		t.Fatalf("send pending placements: %v", err)
	}
	return sent
}

func (f *deployFixture) inventoryMessages(t *testing.T, entries ...*agentv1.InventoryEntry) []*agentv1.SyncResponse {
	t.Helper()
	var sent []*agentv1.SyncResponse
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	if err := session.reconcileInventory(t.Context(), &agentv1.Inventory{Entries: entries}); err != nil {
		t.Fatalf("reconcile inventory: %v", err)
	}
	return sent
}

func wantSuspendedError(t *testing.T, err error) {
	t.Helper()
	wantCode(t, err, connect.CodeFailedPrecondition)
	if !strings.Contains(err.Error(), ErrOrgSuspended.Error()) || !strings.Contains(err.Error(), testSuspensionReason) {
		t.Fatalf("error = %v, want the suspension and its reason", err)
	}
}

func TestSuspendAndResumeRequireSystemAdmin(t *testing.T) {
	f := newDeployFixture(t)
	orgID := f.organizationID(t)
	orgAdmin := secretAuthzContext(t, genDb.EntityScope{
		EntityType: genDb.EntityTypeOrganization,
		EntityID:   uuid.MustParse(orgID),
		Scope:      genDb.ScopeAdmin,
	})
	systemWriter := secretAuthzContext(
		t,
		genDb.EntityScope{EntityType: genDb.EntityTypeSystem, Scope: genDb.ScopeWrite},
	)
	server := NewOrgServer(f.pool, f.queries, nil)

	for name, ctx := range map[string]context.Context{"org admin": orgAdmin, "system writer": systemWriter} {
		_, err := server.SuspendOrg(ctx, &orgv1.SuspendOrgRequest{OrgId: orgID, Reason: testSuspensionReason})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s suspended the organization: %v", name, err)
		}
		_, err = server.ResumeOrg(ctx, &orgv1.ResumeOrgRequest{OrgId: orgID})
		if codeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("%s resumed the organization: %v", name, err)
		}
	}
	org, err := f.queries.GetOrgByID(t.Context(), uuid.MustParse(orgID))
	if err != nil {
		t.Fatal(err)
	}
	if org.SuspendedAt != nil {
		t.Fatal("a denied request suspended the organization")
	}
}

func TestSuspendOrgOfAnUnknownOrganizationIsNotFound(t *testing.T) {
	f := newDeployFixture(t)
	server := NewOrgServer(f.pool, f.queries, nil)
	req := &orgv1.SuspendOrgRequest{OrgId: uuid.NewString(), Reason: testSuspensionReason}
	_, err := server.SuspendOrg(systemAdminContext(t), req)
	wantCode(t, err, connect.CodeNotFound)
}

func TestSuspendAndResumeAreIdempotent(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	first := f.mustSuspend(t)
	if first.GetSuspendedAt() == nil || first.GetSuspensionReason() != testSuspensionReason {
		t.Fatalf("suspended org = %v, want suspended for %q", first, testSuspensionReason)
	}
	again, err := f.suspend(t, otherSuspensionReason)
	if err != nil {
		t.Fatalf("suspend again: %v", err)
	}
	repeated := again.GetOrganization()
	if repeated.GetSuspensionReason() != testSuspensionReason ||
		!repeated.GetSuspendedAt().AsTime().Equal(first.GetSuspendedAt().AsTime()) {
		t.Fatalf("a repeated suspension changed the org to %v", repeated)
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != 2 {
		t.Fatalf("desired revision after two suspensions = %d, want 2", got)
	}
	suspended := f.orgEvents(t, events.OrgSuspended)
	if len(suspended) != 1 {
		t.Fatalf("%d org.suspended events, want 1", len(suspended))
	}
	var data map[string]string
	if err := json.Unmarshal(suspended[0].Data, &data); err != nil || data[events.FieldReason] != testSuspensionReason {
		t.Fatalf("org.suspended data = %s (%v), want the reason", suspended[0].Data, err)
	}

	resumed := f.mustResume(t)
	if resumed.GetSuspendedAt() != nil || resumed.GetSuspensionReason() != "" {
		t.Fatalf("resumed org = %v, want no suspension", resumed)
	}
	f.mustResume(t)
	if got := f.placement(t, f.clusterID).DesiredRevision; got != 3 {
		t.Fatalf("desired revision after two resumes = %d, want 3", got)
	}
	if n := len(f.orgEvents(t, events.OrgResumed)); n != 1 {
		t.Fatalf("%d org.resumed events, want 1", n)
	}
}

func TestSuspensionStopsThePlacementAndResumeRestoresIt(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	server := f.agentServer()
	deploymentID, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	running := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, applied(running.ID, 1))
	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: running.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status = %s, want running", status)
	}

	f.mustSuspend(t)
	stopped := f.placement(t, f.clusterID)
	if stopped.DesiredDeleted || !bytes.Equal(stopped.DesiredSpec, running.DesiredSpec) ||
		stopped.DesiredRevision != 2 {
		t.Fatalf("suspended placement = deleted %v spec %q rev %d, want the spec kept at rev 2",
			stopped.DesiredDeleted, stopped.DesiredSpec, stopped.DesiredRevision)
	}
	if stopped.Ready || stopped.ReadyReplicas != 0 {
		t.Fatalf("suspended placement reports ready %v with %d replicas", stopped.Ready, stopped.ReadyReplicas)
	}
	sent := f.pendingMessages(t)
	if len(sent) != 1 || sent[0].GetDelete().GetPlacementId() != running.ID.String() ||
		sent[0].GetDelete().GetRevision() != 2 {
		t.Fatalf("sent %v, want a Delete of the placement at revision 2", sent)
	}

	server.recordApplied(ctx, f.clusterID, applied(running.ID, 2))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 2 {
		t.Fatalf("applied revision after the stop was acked = %d, want 2", got.AppliedRevision)
	}
	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: running.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if got := f.placement(t, f.clusterID); got.Ready || got.ReadyReplicas != 0 {
		t.Fatal("a late status report marked the suspended placement ready")
	}
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status while suspended = %s, want running", status)
	}
	if acked := f.pendingMessages(t); len(acked) != 0 {
		t.Fatalf("sent %v after the stop was acked", acked)
	}
	leftover := &agentv1.InventoryEntry{PlacementId: running.ID.String(), Revision: 1}
	if present := f.inventoryMessages(t, leftover); len(present) != 1 || present[0].GetDelete() == nil {
		t.Fatalf("sent %v for an application still on the cluster, want a Delete", present)
	}
	if absent := f.inventoryMessages(t); len(absent) != 0 {
		t.Fatalf("sent %v for a stopped application", absent)
	}

	f.mustResume(t)
	sent = f.pendingMessages(t)
	if len(sent) != 1 || sent[0].GetApply().GetRevision() != 3 ||
		!bytes.Equal(sent[0].GetApply().GetApplication(), running.DesiredSpec) {
		t.Fatalf("sent %v, want an Apply of the original spec at revision 3", sent)
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after suspend and resume, want 1", n)
	}
}

func TestSecretChangeWhileSuspendedKeepsTheDeploymentStatus(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	deploymentID, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if _, execErr := f.pool.Exec(ctx,
		`UPDATE placements SET secret_names = '{TOKEN}' WHERE resource_id = $1`, f.resourceID,
	); execErr != nil {
		t.Fatal(execErr)
	}
	running := `UPDATE deployments SET status = 'running' WHERE id = $1`
	if _, execErr := f.pool.Exec(ctx, running, deploymentID); execErr != nil {
		t.Fatal(execErr)
	}
	f.mustSuspend(t)

	env, err := f.queries.GetEnvironmentByID(ctx, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	target := secretTarget{env: env, resourceID: f.resourceID}
	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return rollPlacementsForSecrets(ctx, qtx, target, []string{"TOKEN"})
	})
	if err != nil {
		t.Fatalf("roll placements: %v", err)
	}
	if got := f.placement(t, f.clusterID).DesiredRevision; got != 3 {
		t.Fatalf("desired revision = %d, want the secret change recorded at 3", got)
	}
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status = %s, want running while suspended", status)
	}
	if sent := f.pendingMessages(t); len(sent) != 1 || sent[0].GetDelete() == nil {
		t.Fatalf("sent %v, want the secret change sent as a Delete while suspended", sent)
	}
}

func TestDeploymentsAreRejectedWhileSuspended(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	f.finishRollouts(t)
	f.mustSuspend(t)

	_, err := createDeployment(t, f, sameRegionSpec)
	wantSuspendedError(t, err)
	wantSuspendedError(t, scaleResource(t, f, 2))
	_, err = f.deploy(t.Context(), staticSpec)
	wantSuspendedError(t, err)
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 1 {
		t.Fatalf("%d deployments after rejected deploys, want 1", n)
	}

	f.mustResume(t)
	f.finishRollouts(t)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatalf("scale after resume: %v", err)
	}
}

func TestApplyIsRejectedWhileSuspended(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.mustSuspend(t)

	_, err := applyFile(t, f, planFileHeader+applyFileWorker, applyOptions{}, f.applyScopes(t))
	wantSuspendedError(t, err)
	if f.hasResource(t, "worker") {
		t.Fatal("a rejected apply created the service")
	}
}

func TestDeployCommittedWhileSuspendWaitsIsStopped(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(
			context.Background(),
		); rollbackErr != nil &&
			!errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", rollbackErr)
		}
	}()
	qtx := genDb.New(tx)
	if err := lockOrganization(ctx, qtx, f.workspaceID(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := createDeploymentWithCleanup(ctx, qtx, f.paramsFor(f.clusterID), staticSpec); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, suspendErr := f.suspend(t, testSuspensionReason)
		done <- suspendErr
	}()
	f.waitForLockWaiters(t, 1)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("suspend: %v", err)
	}

	if got := f.placement(t, f.clusterID).DesiredRevision; got != 2 {
		t.Fatalf("desired revision = %d, want the deploy's placement stopped at 2", got)
	}
	sent := f.pendingMessages(t)
	if len(sent) != 1 || sent[0].GetDelete() == nil {
		t.Fatalf("sent %v, want the racing deploy's placement sent as a Delete", sent)
	}
}

func TestSuspensionCancelsBuildsAndRefusesNewOnes(t *testing.T) {
	f := newBuildFixture(t)
	queued := f.insertBuild(t, genDb.BuildStatusQueued)
	running := f.insertBuild(t, genDb.BuildStatusRunning)
	succeeded := f.insertBuild(t, genDb.BuildStatusSucceeded)
	awaiting := f.insertBuild(t, genDb.BuildStatusAwaitingUpload)

	f.mustSuspend(t)
	for _, id := range []string{queued, running, awaiting} {
		build := f.row(t, id)
		if build.Status != genDb.BuildStatusCanceled || build.Message != suspendedBuildMessage {
			t.Fatalf("build %s = %s %q, want canceled by the suspension", id, build.Status, build.Message)
		}
	}
	if build := f.row(t, succeeded); build.Status != genDb.BuildStatusSucceeded {
		t.Fatalf("finished build = %s, want it kept", build.Status)
	}

	fresh := f.insertBuild(t, genDb.BuildStatusAwaitingUpload)
	resourceID := f.row(t, fresh).ResourceID
	_, err := queueBuild(f.ctx, f.pool, uuid.MustParse(fresh), resourceID, f.clusterID)
	wantSuspendedError(t, err)
}
