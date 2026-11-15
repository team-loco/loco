package service

import (
	"encoding/json"
	"maps"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	failedDeployMessage = "the new version stopped before it became ready"
	failedDeployUser    = "Deploy Owner"
	failedDeployBuild   = "01a1de35-0000-7000-8000-000000000001"
	// deployFixtureEmail is the email of the user newDeployFixture creates.
	deployFixtureEmail = "test@example.com"
)

type deploymentFailedEvent struct {
	actorType string
	actorID   uuid.UUID
	envID     uuid.UUID
	data      map[string]any
}

func (f *deployFixture) deploymentFailedEvents(t *testing.T, id uuid.UUID) []deploymentFailedEvent {
	t.Helper()
	rows, err := f.pool.Query(t.Context(),
		`SELECT actor_type, actor_id, environment_id, data FROM events
		 WHERE type = $1 AND subject_type = $2 AND subject_id = $3 ORDER BY seq`,
		events.DeploymentFailed, events.SubjectDeployment, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []deploymentFailedEvent
	for rows.Next() {
		var ev deploymentFailedEvent
		var raw []byte
		if scanErr := rows.Scan(&ev.actorType, &ev.actorID, &ev.envID, &raw); scanErr != nil {
			t.Fatal(scanErr)
		}
		if jsonErr := json.Unmarshal(raw, &ev.data); jsonErr != nil {
			t.Fatal(jsonErr)
		}
		out = append(out, ev)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatal(rowsErr)
	}
	return out
}

// deployAs creates a deployment of a build attributed to the given actor.
func (f *deployFixture) deployAs(
	t *testing.T,
	actorType genDb.DeploymentActorType,
	actorID uuid.UUID,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := withTx(t.Context(), f.pool, func(qtx *genDb.Queries) error {
		counts, err := f.counts(t.Context(), qtx)
		if err != nil {
			return err
		}
		params := f.paramsFor(f.clusterID)
		params.ActorType = actorType
		params.ActorID = actorID
		params.Spec = []byte(`{"build":{"type":"dockerfile","image":"registry.test/svc@sha256:0","buildId":"` +
			failedDeployBuild + `"}}`)
		id, err = createDeploymentWithCleanup(t.Context(), qtx, counts, params, staticSpec)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *deployFixture) failedDeployUser(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(),
		`UPDATE users SET name = $1 WHERE email = $2 RETURNING id`, failedDeployUser, deployFixtureEmail,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func wantDeploymentFailedData(t *testing.T, f *deployFixture, got map[string]any, id, creator uuid.UUID) {
	t.Helper()
	want := map[string]any{
		events.FieldDeploymentID:  id.String(),
		events.FieldResourceID:    f.resourceID.String(),
		events.FieldEnvironmentID: f.envID.String(),
		events.FieldBuildID:       failedDeployBuild,
		events.FieldRegion:        f.paramsFor(f.clusterID).Region,
		events.FieldMessage:       failedDeployMessage,
		events.FieldResourceName:  planSvc,
		events.FieldCreatorID:     creator.String(),
		events.FieldCreatorEmail:  deployFixtureEmail,
		events.FieldCreatorName:   failedDeployUser,
	}
	if !maps.Equal(got, want) {
		t.Fatalf("deployment.failed data = %v, want %v", got, want)
	}
}

func TestDeploymentFailedRecordedOnceAcrossRepeatedReports(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	userID := f.failedDeployUser(t)
	id := f.deployAs(t, genDb.DeploymentActorTypeUser, userID)
	placement := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		Phase:            locov1alpha1.ApplicationPhaseFailed,
		Message:          failedDeployMessage,
	}
	// Separate servers model API replicas reporting the same failure concurrently.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report) })
	}
	wg.Wait()
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report)

	if status := f.deploymentStatus(t, id); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}
	got := f.deploymentFailedEvents(t, id)
	if len(got) != 1 {
		t.Fatalf("deployment.failed events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.actorType != string(genDb.DeploymentActorTypeUser) || ev.actorID != userID || ev.envID != f.envID {
		t.Fatalf("deployment.failed actor = %s/%s, environment %s", ev.actorType, ev.actorID, ev.envID)
	}
	wantDeploymentFailedData(t, f, ev.data, id, userID)
}

func TestDeploymentFailedFromApplyErrorNamesTheTokenAndItsCreator(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	userID := f.failedDeployUser(t)
	var tokenID uuid.UUID
	if err := f.pool.QueryRow(t.Context(),
		`INSERT INTO api_tokens (token_hash, name, entity_type, entity_id, scopes, created_by, expires_at)
		 VALUES ('hash', 'ci', 'resource', $1, '[]', $2, NOW() + INTERVAL '1 day') RETURNING id`,
		f.resourceID, userID,
	).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE resources SET max_apply_attempts = 1 WHERE id = $1`, f.resourceID); err != nil {
		t.Fatal(err)
	}
	id := f.deployAs(t, genDb.DeploymentActorTypeApiToken, tokenID)
	placement := f.placement(t, f.clusterID)
	server := f.agentServer()
	for range 3 {
		server.recordApplied(t.Context(), f.clusterID, &agentv1.Applied{
			PlacementId: placement.ID.String(),
			Revision:    placement.DesiredRevision,
			Error:       failedDeployMessage,
		})
	}

	got := f.deploymentFailedEvents(t, id)
	if len(got) != 1 {
		t.Fatalf("deployment.failed events = %d, want 1", len(got))
	}
	ev := got[0]
	if ev.actorType != string(genDb.DeploymentActorTypeApiToken) || ev.actorID != tokenID {
		t.Fatalf("deployment.failed actor = %s/%s, want api_token/%s", ev.actorType, ev.actorID, tokenID)
	}
	wantDeploymentFailedData(t, f, ev.data, id, userID)
}

func TestDeploymentFailedNotRecordedForOtherOutcomes(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	userID := f.failedDeployUser(t)
	succeeded := f.deployAs(t, genDb.DeploymentActorTypeUser, userID)
	placement := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      placement.ID.String(),
		ObservedRevision: placement.DesiredRevision,
		Ready:            true,
	})
	canceled := f.deployAs(t, genDb.DeploymentActorTypeUser, userID)
	latest := f.deployAs(t, genDb.DeploymentActorTypeUser, userID)
	for id, want := range map[uuid.UUID]genDb.DeploymentStatus{
		succeeded: genDb.DeploymentStatusSucceeded,
		canceled:  genDb.DeploymentStatusCanceled,
		latest:    genDb.DeploymentStatusPending,
	} {
		if status := f.deploymentStatus(t, id); status != want {
			t.Fatalf("deployment %s status = %s, want %s", id, status, want)
		}
		if got := f.deploymentFailedEvents(t, id); len(got) != 0 {
			t.Fatalf("deployment %s (%s) recorded deployment.failed: %v", id, want, got)
		}
	}
}
