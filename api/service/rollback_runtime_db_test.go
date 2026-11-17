package service

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func rollbackDeployment(t *testing.T, f *deployFixture, enabled bool, override *bool) uuid.UUID {
	t.Helper()
	resourceSpec := sameRegionSpec
	if enabled {
		resourceSpec = strings.Replace(resourceSpec, "{", `{"rollbackOnFailure":true,`, 1)
	}
	spec := imageService()
	spec.RollbackOnFailure = override
	response, err := createDeploymentWith(t, f, resourceSpec, spec)
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(response.GetDeploymentId())
}

func reportRollback(t *testing.T, f *deployFixture, available, failed bool) {
	t.Helper()
	p := f.placement(t, f.clusterID)
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
		PlacementId:              p.ID.String(),
		ObservedRevision:         p.DesiredRevision,
		Available:                available,
		ProgressDeadlineExceeded: failed,
	})
}

func TestAutomaticRollback(t *testing.T) {
	t.Parallel()
	for _, applyFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "apply"}[applyFailure], func(t *testing.T) {
			f := newDeployFixture(t)
			previous := rollbackDeployment(t, f, true, nil)
			reportRollback(t, f, true, false)
			failed := rollbackDeployment(t, f, true, nil)
			placement := f.placement(t, f.clusterID)
			if applyFailure {
				if _, err := f.pool.Exec(
					t.Context(),
					`UPDATE resources SET max_apply_attempts=1 WHERE id=$1`,
					f.resourceID,
				); err != nil {
					t.Fatal(err)
				}
				f.agentServer().
					recordApplied(t.Context(), f.clusterID, &agentv1.Applied{
						PlacementId: placement.ID.String(),
						Revision:    placement.DesiredRevision,
						Error:       testApplyFailure,
					})
			} else {
				reportRollback(t, f, false, true)
			}
			restored := f.placement(t, f.clusterID)
			if restored.DeploymentID == nil || *restored.DeploymentID != previous ||
				restored.DesiredRevision <= placement.DesiredRevision {
				t.Fatalf("placement=%+v", restored)
			}
			for _, expected := range []struct {
				id     uuid.UUID
				status genDb.DeploymentStatus
				active bool
			}{
				{failed, genDb.DeploymentStatusFailed, false}, {previous, genDb.DeploymentStatusDeploying, true},
			} {
				row, err := f.queries.GetDeploymentByID(t.Context(), expected.id)
				if err != nil || row.Status != expected.status || row.IsActive != expected.active {
					t.Fatalf("deployment=%+v %v", row, err)
				}
			}
			guardErr := withTx(t.Context(), f.pool, func(qtx *genDb.Queries) error {
				return ensureNoRollout(t.Context(), qtx, f.resourceID, f.envID, testRegion)
			})
			if !errors.Is(guardErr, errRolloutInProgress) {
				t.Fatalf("restored rollout guard=%v", guardErr)
			}
			var messages []string
			for _, id := range []uuid.UUID{failed, previous} {
				rows, err := f.queries.ListDeploymentActivity(
					t.Context(),
					genDb.ListDeploymentActivityParams{DeploymentID: id, PageSize: 200},
				)
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					messages = append(messages, row.Message)
				}
			}
			if !slices.Contains(messages, "Rolled back to "+previous.String()[:8]) ||
				!slices.Contains(messages, "Restored after "+failed.String()[:8]+" failed") {
				t.Fatalf("activity=%v", messages)
			}
			var ready int
			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.ready' AND subject_id=$1`, previous).
				Scan(&ready); err != nil ||
				ready != 1 {
				t.Fatalf("ready=%d %v", ready, err)
			}
			client := f.watchClient(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
			defer cancel()
			stream, err := client.WatchDeployment(
				ctx,
				&deploymentv1.WatchDeploymentRequest{DeploymentId: failed.String()},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = stream.Receive(); err != nil {
				t.Fatal(err)
			}
			if _, err = stream.Receive(); !errors.Is(err, io.EOF) {
				t.Fatalf("failed watch=%v", err)
			}
			if err = stream.Close(); err != nil {
				t.Fatal(err)
			}
			reportRollback(t, f, true, false)
			if got := f.deploymentStatus(t, previous); got != genDb.DeploymentStatusRunning {
				t.Fatalf("restored=%s", got)
			}
			if got := f.deploymentStatus(t, failed); got != genDb.DeploymentStatusFailed {
				t.Fatalf("failed=%s", got)
			}
			reportRollback(t, f, true, false)
			if readyErr := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.ready' AND subject_id=$1`, previous).Scan(&ready); readyErr != nil || ready != 2 {
				t.Fatalf("restored ready events=%d %v", ready, readyErr)
			}

			if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.failed' AND subject_id=$1`, failed).
				Scan(&ready); err != nil ||
				ready != 1 {
				t.Fatalf("failed events=%d %v", ready, err)
			}
		})
	}
}

func TestRollbackSettingResolutionAndNoPrevious(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		service  bool
		override *bool
		previous bool
		rollback bool
	}{
		{"default off", false, nil, true, false}, {"service on", true, nil, true, true},
		{"override off", true, new(false), true, false}, {"override on", false, new(true), true, true},
		{"first deployment", true, nil, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newDeployFixture(t)
			if tt.previous {
				rollbackDeployment(t, f, tt.service, nil)
				reportRollback(t, f, true, false)
			}
			id := rollbackDeployment(t, f, tt.service, tt.override)
			reportRollback(t, f, false, true)
			row, err := f.queries.GetDeploymentByID(t.Context(), id)
			if err != nil || row.IsActive == tt.rollback {
				t.Fatalf("failed=%+v %v", row, err)
			}
			if !tt.rollback {
				reportRollback(t, f, true, false)
				if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusRunning {
					t.Fatalf("recovery=%s", got)
				}
			}
		})
	}
}

func TestRestoredDeploymentCannotRollBackAgain(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	previous := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	failed := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, false, true)
	revision := f.placement(t, f.clusterID).DesiredRevision
	reportRollback(t, f, false, true)
	reportRollback(t, f, false, true)
	current := f.placement(t, f.clusterID)
	if current.DeploymentID == nil || *current.DeploymentID != previous || current.DesiredRevision != revision {
		t.Fatalf("recursive rollback=%+v", current)
	}
	if got := f.deploymentStatus(t, failed); got != genDb.DeploymentStatusFailed {
		t.Fatalf("original failure=%s", got)
	}
	rollbackDeployment(t, f, false, nil)
	reportRollback(t, f, true, false)
	if got := f.deploymentStatus(t, previous); got != genDb.DeploymentStatusSuperseded {
		t.Fatalf("restored failure after a new deployment=%s", got)
	}
	if got := f.deploymentStatus(t, failed); got != genDb.DeploymentStatusFailed {
		t.Fatalf("final failed attempt=%s", got)
	}
}

func TestBuildFailureDoesNotRollBack(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	previous := rollbackDeployment(t, f.deployFixture, true, nil)
	reportRollback(t, f.deployFixture, true, false)
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	if _, err := f.pool.Exec(
		f.ctx,
		`UPDATE resources SET spec=jsonb_set(spec,'{rollbackOnFailure}','true') WHERE id=$1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	status := buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_FAILED)
	if err := f.agentServer().recordBuildStatus(f.ctx, f.clusterID, uuid.MustParse(buildID), status); err != nil {
		t.Fatal(err)
	}
	row, err := f.queries.GetDeploymentByID(f.ctx, id)
	if err != nil || row.Status != genDb.DeploymentStatusFailed || !row.IsActive {
		t.Fatalf("build failure=%+v %v", row, err)
	}
	if p := f.placement(t, f.clusterID); p.DeploymentID == nil || *p.DeploymentID != previous {
		t.Fatalf("placement=%+v", p)
	}
}

func TestRollbackSkipsHistoryThatNeverRan(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, false, true)
	current := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, false, true)
	row, err := f.queries.GetDeploymentByID(t.Context(), current)
	if err != nil || !row.IsActive || row.Status != genDb.DeploymentStatusFailed {
		t.Fatalf("failed=%+v %v", row, err)
	}
}

func TestRestoredRunningSupersedesOnlyOlderDeployments(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	older := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	previous := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	failed := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, true, false)
	reportRollback(t, f, false, true)
	reportRollback(t, f, true, false)
	for id, expected := range map[uuid.UUID]genDb.DeploymentStatus{
		older:    genDb.DeploymentStatusSuperseded,
		previous: genDb.DeploymentStatusRunning,
		failed:   genDb.DeploymentStatusFailed,
	} {
		if got := f.deploymentStatus(t, id); got != expected {
			t.Fatalf("%s=%s, want %s", id, got, expected)
		}
	}
}

func TestInactiveFailedDeploymentWatchEnds(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	failed := rollbackDeployment(t, f, false, nil)
	reportRollback(t, f, true, false)
	reportRollback(t, f, false, true)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := f.watchClient(t).WatchDeployment(
		ctx,
		&deploymentv1.WatchDeploymentRequest{DeploymentId: failed.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if _, err = stream.Receive(); err != nil {
		t.Fatal(err)
	}
	rollbackDeployment(t, f, false, nil)
	final, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if final.GetDeployment().GetIsActive() ||
		final.GetDeployment().GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
		t.Fatalf("final deployment=%v", final.GetDeployment())
	}
	if _, err = stream.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("failed watch=%v", err)
	}
}

func TestRollbackUsesCurrentServiceSetting(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	previous := rollbackDeployment(t, f, false, nil)
	reportRollback(t, f, true, false)
	rollbackDeployment(t, f, false, nil)
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE resources SET spec=jsonb_set(spec,'{rollbackOnFailure}','true') WHERE id=$1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
	reportRollback(t, f, false, true)
	if current := f.placement(t, f.clusterID); current.DeploymentID == nil || *current.DeploymentID != previous {
		t.Fatalf("placement=%+v", current)
	}
}

func TestRegionEditAfterRollbackUsesRestoredSpec(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	const restoredUser int64 = 1001
	for _, user := range []int64{restoredUser, 2001} {
		spec := imageService()
		spec.RollbackOnFailure = new(true)
		spec.RunAsUser = new(user)
		spec.Env = map[string]string{"VERSION": "kept-runtime-env"}
		if _, err := createDeploymentWith(t, f, sameRegionSpec, spec); err != nil {
			t.Fatal(err)
		}
		reportRollback(t, f, user == restoredUser, user != restoredUser)
	}
	reportRollback(t, f, true, false)
	targets, revision := regionTargets(t, f)
	targets[testRegion].Cpu = biggerCPU
	if err := updateResourceRegions(t, f, targets, revision); err != nil {
		t.Fatal(err)
	}
	payload := desiredPayload(t, f)
	if got := payload.AppSpec.ServiceSpec.Deployment.RunAsUser; got == nil || *got != restoredUser {
		t.Fatalf("resized runtime user=%v", got)
	}
	if got := payload.AppSpec.ServiceSpec.Deployment.Env["VERSION"]; got != "kept-runtime-env" {
		t.Fatalf("resized deployment env=%q", got)
	}
}
