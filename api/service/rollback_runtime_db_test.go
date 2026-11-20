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

// liveDeployment is the deployment the fixture cluster's placement runs.
func (f *deployFixture) liveDeployment(t *testing.T) uuid.UUID {
	t.Helper()
	p := f.placement(t, f.clusterID)
	if p.DeploymentID == nil {
		t.Fatalf("placement %s runs no deployment", p.ID)
	}
	return *p.DeploymentID
}

func reportRollback(t *testing.T, f *deployFixture, available, failed bool) {
	t.Helper()
	p := f.placement(t, f.clusterID)
	report := &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Available:        available,
		Healthy:          available,
	}
	if failed {
		report.Stopped = deployTimedOut()
	}
	f.agentServer().recordStatus(t.Context(), f.clusterID, 0, report)
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
				for range testApplyAttempts {
					f.agentServer().
						recordApplied(t.Context(), f.clusterID, &agentv1.Applied{
							PlacementId: placement.ID.String(),
							Revision:    placement.DesiredRevision,
							Error:       testApplyFailure,
						})
				}
			} else {
				reportRollback(t, f, false, true)
			}
			placed := f.placement(t, f.clusterID)
			rollback := f.liveDeployment(t)
			if rollback == previous || rollback == failed || placed.DesiredRevision <= placement.DesiredRevision {
				t.Fatalf("placement=%+v, want a new rollback deployment", placed)
			}
			restored, err := f.queries.GetDeploymentByID(t.Context(), rollback)
			if err != nil || restored.Trigger != genDb.DeploymentTriggerAutoRollback ||
				restored.SourceDeploymentID == nil || *restored.SourceDeploymentID != previous ||
				restored.Status != genDb.DeploymentStatusDeploying {
				t.Fatalf("rollback=%+v %v, want a deploying automatic rollback copying %s", restored, err, previous)
			}
			if got := f.deploymentStatus(t, failed); got != genDb.DeploymentStatusFailed {
				t.Fatalf("failed=%s", got)
			}
			var messages []string
			for _, id := range []uuid.UUID{failed, rollback} {
				rows, listErr := f.queries.ListDeploymentActivity(
					t.Context(),
					genDb.ListDeploymentActivityParams{DeploymentID: id, PageSize: 200},
				)
				if listErr != nil {
					t.Fatal(listErr)
				}
				for _, row := range rows {
					messages = append(messages, row.Message)
				}
			}
			if !slices.Contains(messages, "Rolled back to "+previous.String()[28:]) ||
				!slices.Contains(messages, "Restored after "+failed.String()[28:]+" failed") {
				t.Fatalf("activity=%v", messages)
			}
			var ready int
			if readErr := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.ready' AND subject_id=$1`, previous).
				Scan(&ready); readErr != nil ||
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
			for id, want := range map[uuid.UUID]genDb.DeploymentStatus{
				rollback: genDb.DeploymentStatusRunning,
				previous: genDb.DeploymentStatusSuperseded,
				failed:   genDb.DeploymentStatusFailed,
			} {
				if got := f.deploymentStatus(t, id); got != want {
					t.Fatalf("%s=%s, want %s", id, got, want)
				}
			}
			reportRollback(t, f, true, false)
			if readyErr := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.ready' AND subject_id=$1`, rollback).Scan(&ready); readyErr != nil || ready != 1 {
				t.Fatalf("rollback ready events=%d %v", ready, readyErr)
			}

			if readErr := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM events
 WHERE type='deployment.failed' AND subject_id=$1`, failed).
				Scan(&ready); readErr != nil ||
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
			if rolledBack := f.liveDeployment(t) != id; rolledBack != tt.rollback {
				t.Fatalf("rolled back=%v, want %v", rolledBack, tt.rollback)
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
	rollback := f.liveDeployment(t)
	revision := f.placement(t, f.clusterID).DesiredRevision
	reportRollback(t, f, false, true)
	reportRollback(t, f, false, true)
	current := f.placement(t, f.clusterID)
	if rollback == failed || *current.DeploymentID != rollback || current.DesiredRevision != revision {
		t.Fatalf("recursive rollback=%+v", current)
	}
	for id, want := range map[uuid.UUID]genDb.DeploymentStatus{
		failed: genDb.DeploymentStatusFailed, rollback: genDb.DeploymentStatusFailed,
	} {
		if got := f.deploymentStatus(t, id); got != want {
			t.Fatalf("%s=%s, want %s", id, got, want)
		}
	}
	rollbackDeployment(t, f, false, nil)
	reportRollback(t, f, true, false)
	if got := f.deploymentStatus(t, previous); got != genDb.DeploymentStatusSuperseded {
		t.Fatalf("previous after a new deployment=%s", got)
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
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusFailed {
		t.Fatalf("build failure=%s", got)
	}
	if live := f.liveDeployment(t); live != previous {
		t.Fatalf("placement runs %s, want the previous %s", live, previous)
	}
}

func TestRollbackSkipsHistoryThatNeverRan(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, false, true)
	current := rollbackDeployment(t, f, true, nil)
	reportRollback(t, f, false, true)
	if live := f.liveDeployment(t); live != current || f.deploymentStatus(t, current) != genDb.DeploymentStatusFailed {
		t.Fatalf("live=%s, want the failed %s", live, current)
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
	rollback := f.liveDeployment(t)
	reportRollback(t, f, true, false)
	for id, expected := range map[uuid.UUID]genDb.DeploymentStatus{
		older:    genDb.DeploymentStatusSuperseded,
		previous: genDb.DeploymentStatusSuperseded,
		failed:   genDb.DeploymentStatusFailed,
		rollback: genDb.DeploymentStatusRunning,
	} {
		if got := f.deploymentStatus(t, id); got != expected {
			t.Fatalf("%s=%s, want %s", id, got, expected)
		}
	}
}

func TestFailedDeploymentWatchEnds(t *testing.T) {
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
	final, err := stream.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if final.GetDeployment().GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED {
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
	restored, err := f.queries.GetDeploymentByID(t.Context(), f.liveDeployment(t))
	if err != nil || restored.SourceDeploymentID == nil || *restored.SourceDeploymentID != previous {
		t.Fatalf("live deployment=%+v %v, want a rollback to %s", restored, err, previous)
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
