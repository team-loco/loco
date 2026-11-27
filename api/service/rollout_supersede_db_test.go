package service

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/proto"
)

const rolloutDeleteOperation = "delete"

func (f *deployFixture) rolloutContext(t *testing.T) context.Context {
	t.Helper()
	var userID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: userID}
	return reqctx.WithPrincipal(
		t.Context(),
		reqctx.Principal{
			ActorType: genDb.DeploymentActorTypeUser,
			Method:    genDb.DeploymentMethodWeb,
			Entity:    entity,
			Scopes:    scopes,
		},
	)
}

func (f *deployFixture) reportRollout(t *testing.T, ready bool) {
	t.Helper()
	p := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Available:        ready,
		Healthy:          ready,
		Deployed:         ready,
		ReadyReplicas:    1,
	})
}

func (f *deployFixture) rolloutSnapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	query := `SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM deployments d),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM placements p),
 (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM events e),
 (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.name) FROM secrets s),
 (SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM environments e))::text`
	if err := f.pool.QueryRow(t.Context(), query).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *deployFixture) finishRollouts(t *testing.T) {
	t.Helper()
	rows, err := f.pool.Query(
		t.Context(),
		`SELECT id, cluster_id, desired_revision FROM placements WHERE NOT desired_deleted`,
	)
	if err != nil {
		t.Fatal(err)
	}
	type report struct {
		placementID, clusterID uuid.UUID
		revision               int64
	}
	var reports []report
	for rows.Next() {
		var r report
		if scanErr := rows.Scan(&r.placementID, &r.clusterID, &r.revision); scanErr != nil {
			rows.Close()
			t.Fatal(scanErr)
		}
		reports = append(reports, r)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	for _, r := range reports {
		f.agentServer().
			recordStatus(t.Context(), r.clusterID, 0, &agentv1.PlacementStatus{
				PlacementId:      r.placementID.String(),
				ObservedRevision: r.revision,
				Available:        true,
				Healthy:          true,
				Deployed:         true,
				ReadyReplicas:    1,
			})
	}
}

// wantSuperseded checks that the in-flight deployment ended canceled and a newer one is live.
func (f *deployFixture) wantSuperseded(t *testing.T, inFlight uuid.UUID) {
	t.Helper()
	old, err := f.queries.GetDeploymentByID(t.Context(), inFlight)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != genDb.DeploymentStatusCanceled {
		t.Fatalf("in-flight deployment status=%s, want canceled", old.Status)
	}
	live := f.activeDeployment(t, f.envID)
	p := f.placement(t, f.clusterID)
	if live.ID == inFlight || p.DeploymentID == nil || *p.DeploymentID != live.ID {
		t.Fatalf("live deployment=%s placement=%v, want a newer deployment than %s", live.ID, p.DeploymentID, inFlight)
	}
}

func TestScaleDuringARolloutSupersedesIt(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"pending", "deploying"} {
		t.Run(phase, func(t *testing.T) {
			f := newDeployFixture(t)
			created, err := createTestDeployment(t, f, sameRegionSpec)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "deploying" {
				f.reportRollout(t, false)
			}
			before := f.placement(t, f.clusterID)
			if scaleErr := scaleResource(t, f, 2); scaleErr != nil {
				t.Fatal(scaleErr)
			}
			f.wantSuperseded(t, uuid.MustParse(created.GetDeploymentId()))
			if after := f.placement(t, f.clusterID); after.DesiredRevision <= before.DesiredRevision {
				t.Fatalf("placement revision %d, want above %d", after.DesiredRevision, before.DesiredRevision)
			}
			if replicas := desiredPayload(t, f).AppSpec.ServiceSpec.Resources.Replicas.Min; replicas != 2 {
				t.Fatalf("desired replicas = %d, want 2", replicas)
			}
		})
	}
}

func TestDeploymentDuringARolloutSupersedesIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, false)
	replacement, err := deployService(t, f, imageService())
	if err != nil {
		t.Fatal(err)
	}
	f.wantSuperseded(t, uuid.MustParse(created.GetDeploymentId()))
	if live := f.activeDeployment(t, f.envID); live.ID.String() != replacement.GetDeploymentId() {
		t.Fatalf("live deployment = %s, want %s", live.ID, replacement.GetDeploymentId())
	}
}

func TestRestartDuringARolloutRollsItAgain(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, false)
	before := f.placement(t, f.clusterID)
	for range 2 {
		if restartErr := restartResource(t, f, f.envID, "", genDb.ScopeWrite); restartErr != nil {
			t.Fatal(restartErr)
		}
	}
	after := f.placement(t, f.clusterID)
	if after.DesiredRevision != before.DesiredRevision+2 || after.DeploymentID == nil ||
		after.DeploymentID.String() != created.GetDeploymentId() {
		t.Fatalf("placement revision=%d deployment=%v, want revision %d of %s",
			after.DesiredRevision, after.DeploymentID, before.DesiredRevision+2, created.GetDeploymentId())
	}
}

func TestSecretWritesSucceedDuringARollout(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"set", rolloutDeleteOperation} {
		t.Run(operation, func(t *testing.T) {
			f := newDeployFixture(t)
			provider := newTestKeyProvider(t, "k1")
			f.storeSecret(t, provider, stripeKey, stripeValue)
			if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
				t.Fatal(err)
			}
			server := f.secretServer(provider)
			ctx := f.secretWriterContext(t)
			var err error
			if operation == "set" {
				_, err = server.SetSecrets(ctx, &secretv1.SetSecretsRequest{
					EnvironmentId: f.envID.String(),
					Values:        map[string]string{stripeKey: "new value"},
				})
			} else {
				_, err = server.DeleteSecrets(ctx, &secretv1.DeleteSecretsRequest{
					EnvironmentId: f.envID.String(),
					Names:         []string{stripeKey},
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if operation == rolloutDeleteOperation {
				want = 0
			}
			if n := f.count(t, `SELECT count(*) FROM secrets s JOIN resources r ON r.id = $1
WHERE s.environment_id IN (SELECT id FROM environments WHERE workspace_id = r.workspace_id)`); int64(n) != want {
				t.Fatalf("secrets = %d, want %d", n, want)
			}
		})
	}
}

func TestConcurrentScalesBothApplyInOrder(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, true)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	results := make(chan error, 2)
	for replicas := int32(2); replicas <= 3; replicas++ {
		go func() { results <- scaleResource(t, f, replicas) }()
	}
	dbtest.WaitForLockWaiters(t, f.pool, 2)
	release()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`); n != 3 {
		t.Fatalf("deployments = %d, want 3", n)
	}
	if n := f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1 AND status = 'canceled'`); n != 1 {
		t.Fatalf("canceled deployments = %d, want the superseded scale", n)
	}
	f.activeDeployment(t, f.envID)
}

func TestApplyDuringARolloutSupersedesIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})
	inFlight := f.activeDeployment(t, f.envID)
	applyOK(t, f, planFileHeader+applyFileWorkerDebug, applyOptions{revision: f.revision(t, f.envID)})
	old, err := f.queries.GetDeploymentByID(t.Context(), inFlight.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != genDb.DeploymentStatusCanceled {
		t.Fatalf("in-flight deployment status=%s, want canceled", old.Status)
	}
	if live := f.activeDeployment(t, f.envID); live.ID == inFlight.ID {
		t.Fatal("apply left the in-flight deployment live")
	}
}

func TestAFailedRolloutLeavesTheServiceOpenToChanges(t *testing.T) {
	t.Parallel()
	stoppedRollout := stoppedReport()
	stoppedRollout.Stopped.Rollout = true
	failures := map[string]*agentv1.PlacementStatus{
		"deploy timeout": {
			Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemDeployTimeout)},
			Stopped: deployTimedOut(),
		},
		"restart budget": stoppedRollout,
	}
	changes := map[string]func(t *testing.T, f *deployFixture) error{
		"deploy": func(t *testing.T, f *deployFixture) error {
			t.Helper()
			_, err := deployService(t, f, imageService())
			return err
		},
		"scale": func(t *testing.T, f *deployFixture) error {
			t.Helper()
			return scaleResource(t, f, 2)
		},
		"settings": func(t *testing.T, f *deployFixture) error {
			t.Helper()
			server := NewResourceServer(
				f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults(), nil,
			)
			_, err := server.UpdateResource(f.rolloutContext(t), &resourcev1.UpdateResourceRequest{
				ResourceId: f.resourceID.String(), Name: new("Renamed"),
				FailurePolicy: &deploymentv1.FailurePolicy{
					RestartPolicy: deploymentv1.RestartPolicy_RESTART_POLICY_ALWAYS, DeployTimeoutSeconds: 120,
				},
			})
			return err
		},
	}
	for failure, report := range failures {
		for change, apply := range changes {
			t.Run(failure+" then "+change, func(t *testing.T) {
				t.Parallel()
				f := newDeployFixture(t)
				if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
					t.Fatal(err)
				}
				f.finishRollouts(t)
				failing, err := deployService(t, f, imageService())
				if err != nil {
					t.Fatal(err)
				}
				f.reportStatus(t, proto.CloneOf(report))
				id := uuid.MustParse(failing.GetDeploymentId())
				if status := f.deploymentStatus(t, id); status != genDb.DeploymentStatusFailed {
					t.Fatalf("rollout status = %s, want failed", status)
				}
				before := f.placement(t, f.clusterID).DesiredRevision
				if err := apply(t, f); err != nil {
					t.Fatalf("%s after a failed rollout: %v", change, err)
				}
				if change == "settings" {
					if res, getErr := f.queries.GetResourceByID(t.Context(), f.resourceID); getErr != nil ||
						res.Name != "Renamed" {
						t.Fatalf("resource = %+v (%v), want the new display name", res, getErr)
					}
					return
				}
				if after := f.placement(t, f.clusterID).DesiredRevision; after <= before {
					t.Fatalf("placement revision = %d, want above %d", after, before)
				}
				if live := f.activeDeployment(t, f.envID); live.ID == id {
					t.Fatalf("live deployment = %s, want one newer than the failed rollout", live.ID)
				}
			})
		}
	}
}
