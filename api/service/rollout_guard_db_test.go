package service

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

func (f *deployFixture) rolloutContext(t *testing.T) context.Context {
	t.Helper()
	var userID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeWrite},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	return context.WithValue(ctx, contextkeys.EntityKey, genDb.Entity{Type: genDb.EntityTypeUser, ID: userID})
}

func (f *deployFixture) reportRollout(t *testing.T, phase string, ready bool) {
	t.Helper()
	p := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision, Phase: phase, Ready: ready, ReadyReplicas: 1,
	})
}

func TestScaleRejectsAnActiveRollout(t *testing.T) {
	for _, phase := range []string{"pending", "deploying"} {
		t.Run(phase, func(t *testing.T) {
			f := newDeployFixture(t)
			if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
				t.Fatal(err)
			}
			if phase == "deploying" {
				f.reportRollout(t, "Deploying", false)
			}
			before := f.placement(t, f.clusterID)
			revision := f.revision(t, f.envID)
			wantCode(t, scaleResource(t, f, 2), connect.CodeFailedPrecondition)
			after := f.placement(t, f.clusterID)
			if after.DesiredRevision != before.DesiredRevision || f.revision(t, f.envID) != revision ||
				f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`) != 1 {
				t.Fatal("rejected scaling changed desired state")
			}
		})
	}
}

func TestDeploymentRejectsAnActiveRollout(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	_, err := deployService(t, f, imageService())
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestSecretRotationKeepsTheRolloutBlockedUntilReady(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	f.storeSecret(t, provider, stripeKey, stripeValue)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE placements SET secret_names = $2 WHERE resource_id = $1`,
		f.resourceID,
		[]string{stripeKey},
	); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	server := f.secretServer(provider)
	ctx := f.rolloutContext(t)
	request := &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Values:        map[string]string{stripeKey: "rotated"},
	}
	if _, err := server.SetSecrets(ctx, request); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	if status := f.deploymentStatus(t, *p.DeploymentID); status != genDb.DeploymentStatusDeploying {
		t.Fatalf("rotation status = %s, want deploying", status)
	}
	f.agentServer().
		recordStatus(t.Context(), f.clusterID, &agentv1.PlacementStatus{
			PlacementId:      p.ID.String(),
			ObservedRevision: p.DesiredRevision - 1,
			Phase:            testPhaseReady,
			Ready:            true,
			ReadyReplicas:    1,
		})
	wantCode(t, scaleResource(t, f, 2), connect.CodeFailedPrecondition)
	f.reportRollout(t, "Deploying", false)
	wantCode(t, scaleResource(t, f, 2), connect.CodeFailedPrecondition)
	request.Values[stripeKey] = "another rotation"
	_, err := server.SetSecrets(ctx, request)
	wantCode(t, err, connect.CodeFailedPrecondition)
	f.reportRollout(t, testPhaseReady, true)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
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

func TestSecretWritesRejectAnActiveRolloutWithoutChangingState(t *testing.T) {
	for _, operation := range []string{"set", "delete"} {
		t.Run(operation, func(t *testing.T) {
			f := newDeployFixture(t)
			provider := testLocalProvider(t, "k1")
			f.storeSecret(t, provider, stripeKey, stripeValue)
			if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
				t.Fatal(err)
			}
			before := f.rolloutSnapshot(t)
			server := f.secretServer(provider)
			ctx := f.rolloutContext(t)
			var err error
			if operation == "set" {
				_, err = server.SetSecrets(
					ctx,
					&secretv1.SetSecretsRequest{
						ResourceId:    f.resourceID.String(),
						EnvironmentId: f.envID.String(),
						Values:        map[string]string{stripeKey: "new value"},
					},
				)
			} else {
				_, err = server.DeleteSecrets(
					ctx,
					&secretv1.DeleteSecretsRequest{
						ResourceId:    f.resourceID.String(),
						EnvironmentId: f.envID.String(),
						Names:         []string{stripeKey},
					},
				)
			}
			wantCode(t, err, connect.CodeFailedPrecondition)
			if f.rolloutSnapshot(t) != before {
				t.Fatal("rejected secret mutation changed persisted state")
			}
		})
	}
}

func TestUnchangedSecretRemainsANoopDuringRollout(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	f.storeSecret(t, provider, stripeKey, stripeValue)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	before := f.rolloutSnapshot(t)
	_, err := f.secretServer(provider).
		SetSecrets(f.rolloutContext(t), &secretv1.SetSecretsRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			Values:        map[string]string{stripeKey: stripeValue},
		})
	if err != nil {
		t.Fatal(err)
	}
	if f.rolloutSnapshot(t) != before {
		t.Fatal("unchanged secret modified state")
	}
}

func TestScaleAllowsRecoveryAfterFailedRollout(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, applicationPhaseFailed, false)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentAPIInstancesAcceptOnlyOneScale(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	results := make(chan error, 2)
	for replicas := int32(2); replicas <= 3; replicas++ {
		go func() { results <- scaleResource(t, f, replicas) }()
	}
	f.waitForLockWaiters(t, 2)
	release()
	accepted, rejected := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			accepted++
		case connect.CodeOf(err) == connect.CodeFailedPrecondition:
			rejected++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d, want one of each", accepted, rejected)
	}
	if f.count(t, `SELECT count(*) FROM deployments WHERE resource_id = $1`) != 2 {
		t.Fatal("concurrent scaling created more than one deployment")
	}
}

func TestScaleGuardRespectsTheRequestedRegion(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	params := f.paramsFor(f.otherCluster)
	params.Region = "us-west-2"
	if _, err := f.queries.CreateDeployment(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	before := f.rolloutSnapshot(t)
	wantCode(t, scaleResource(t, f, 2), connect.CodeFailedPrecondition)
	if f.rolloutSnapshot(t) != before {
		t.Fatal("all-region scaling partly mutated the healthy region")
	}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	_, err := server.ScaleResource(
		f.rolloutContext(t),
		&resourcev1.ScaleResourceRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			Region:        new(testRegion),
			Replicas:      new(int32(2)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLaterHealthDegradationDoesNotBlockChanges(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	f.reportRollout(t, "Deploying", false)
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRejectsRuntimeChangesDuringRollout(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileWorker
	applyOK(t, f, file, applyOptions{})
	before := f.rolloutSnapshot(t)
	changed := planFileHeader + applyFileWorkerDebug
	_, err := applyFile(t, f, changed, applyOptions{revision: f.revision(t, f.envID)}, f.adminScopes(t))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if f.rolloutSnapshot(t) != before {
		t.Fatal("rejected apply changed persisted state")
	}
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
			recordStatus(t.Context(), r.clusterID, &agentv1.PlacementStatus{
				PlacementId:      r.placementID.String(),
				ObservedRevision: r.revision,
				Phase:            testPhaseReady,
				Ready:            true,
				ReadyReplicas:    1,
			})
	}
}

func TestOtherEnvironmentRolloutDoesNotBlockScale(t *testing.T) {
	f := newDeployFixture(t)
	stagingID := f.deployToProductionAndStaging(t)
	f.reportRollout(t, testPhaseReady, true)
	staging := f.activeDeployment(t, stagingID)
	if staging.Status != genDb.DeploymentStatusPending {
		t.Fatalf("other environment = %s, want pending", staging.Status)
	}
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
	if other := f.activeDeployment(
		t,
		stagingID,
	); other.ID != staging.ID ||
		other.Status != genDb.DeploymentStatusPending {
		t.Fatal("scaling changed the other environment's rollout")
	}
}

func TestOtherResourceRolloutDoesNotBlockScale(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	resourceID, err := f.queries.CreateResource(
		t.Context(),
		genDb.CreateResourceParams{
			WorkspaceID: f.workspaceID(t),
			Name:        "other",
			Type:        genDb.ResourceTypeService,
			Spec:        []byte("{}"),
			SpecVersion: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	params := f.paramsFor(f.clusterID)
	params.ResourceID = resourceID
	if _, err := f.queries.CreateDeployment(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	if err := scaleResource(t, f, 2); err != nil {
		t.Fatal(err)
	}
}

func TestFailedSecretRotationAllowsAnotherSecretChange(t *testing.T) {
	f := newDeployFixture(t)
	provider := testLocalProvider(t, "k1")
	f.storeSecret(t, provider, stripeKey, stripeValue)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE placements SET secret_names = $2 WHERE resource_id = $1`,
		f.resourceID,
		[]string{stripeKey},
	); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, testPhaseReady, true)
	server := f.secretServer(provider)
	request := &secretv1.SetSecretsRequest{
		ResourceId:    f.resourceID.String(),
		EnvironmentId: f.envID.String(),
		Values:        map[string]string{stripeKey: "bad value"},
	}
	if _, err := server.SetSecrets(f.rolloutContext(t), request); err != nil {
		t.Fatal(err)
	}
	f.reportRollout(t, applicationPhaseFailed, false)
	request.Values[stripeKey] = "repaired value"
	if _, err := server.SetSecrets(f.rolloutContext(t), request); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	if status := f.deploymentStatus(t, *p.DeploymentID); status != genDb.DeploymentStatusDeploying {
		t.Fatalf("replacement rotation = %s, want deploying", status)
	}
}
