package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRolloutActivityReports(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	id, deployErr := f.deploy(ctx, staticSpec)
	if deployErr != nil {
		t.Fatal(deployErr)
	}
	p := f.placement(t, f.clusterID)
	server := f.agentServer()
	at := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	report := &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		DesiredReplicas:  1,
		Replicas: []*agentv1.ReplicaReport{
			{
				Uid:               activityReplicaUID,
				Name:              "pod-a",
				PlacementRevision: p.DesiredRevision,
				State:             "Scheduling",
				StateSince:        timestamppb.New(at),
			},
		},
	}
	server.recordStatus(ctx, f.clusterID, 1, report)
	report.ObservedRevision--
	server.recordStatus(ctx, f.clusterID, 0, report)
	if count := activityRows(t, f, id); count != 0 {
		t.Fatalf("stale reports wrote %d rows", count)
	}
	report.ObservedRevision = p.DesiredRevision
	states := []loco.ReplicaState{
		loco.ReplicaScheduling, loco.ReplicaFailing, loco.ReplicaStarting, loco.ReplicaFailing,
		loco.ReplicaStarting, loco.ReplicaFailing, loco.ReplicaServing,
	}
	for _, state := range states {
		report.Replicas[0].State = string(state)
		if state == loco.ReplicaStarting {
			report.Replicas[0].Restarts++
		}
		server.recordStatus(ctx, f.clusterID, 0, report)
	}
	before := activityRows(t, f, id)
	server.recordStatus(ctx, f.clusterID, 0, report)
	if count := activityRows(t, f, id); count != before {
		t.Fatalf("replay added rows: %d -> %d", before, count)
	}
	for _, invalid := range []string{"state", "problem"} {
		report.Replicas[0].State = string(loco.ReplicaServing)
		report.Problem = nil
		if invalid == "state" {
			report.Replicas[0].State = "not-a-state"
		} else {
			report.Problem = &agentv1.ProblemReport{Reason: "not-a-reason"}
		}
		server.recordStatus(ctx, f.clusterID, 0, report)
		if count := activityRows(t, f, id); count != before {
			t.Fatalf("invalid report added rows: %d -> %d", before, count)
		}
	}
	replicaRows, readErr := f.pool.Query(ctx, `SELECT message,at FROM deployment_activity
 WHERE deployment_id=$1 AND replica_uid=$2 ORDER BY seq`, id, activityReplicaUID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	defer replicaRows.Close()
	index := 0
	for replicaRows.Next() {
		var message string
		var timestamp time.Time
		if err := replicaRows.Scan(&message, &timestamp); err != nil {
			t.Fatal(err)
		}
		if index >= len(states) {
			t.Fatal("extra replica activity")
		}
		want, _ := replicaActivityMessage(&agentv1.ReplicaReport{State: string(states[index])})
		if message != want || !timestamp.Equal(at) {
			t.Fatalf("transition %d = %q at %v", index, message, timestamp)
		}
		index++
	}
	if err := replicaRows.Err(); err != nil {
		t.Fatal(err)
	}
	replicaRows.Close()
	if index != len(states) {
		t.Fatalf("replica transitions=%d want %d", index, len(states))
	}

	rows, err := f.pool.Query(ctx, `SELECT seq FROM deployment_activity WHERE deployment_id=$1 ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seq := int64(0)
	for rows.Next() {
		var got int64
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		seq++
		if got != seq {
			t.Fatalf("seq=%d want %d", got, seq)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := f.pool.Exec(ctx, `DELETE FROM deployments WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if activityRows(t, f, id) != 0 {
		t.Fatal("activity did not cascade")
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM deployment_rollouts WHERE deployment_id=$1`, id).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("rollout cascade %d %v", count, err)
	}
}

func activityRows(t *testing.T, f *deployFixture, id uuid.UUID) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM deployment_activity WHERE deployment_id=$1`, id).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRolloutConcurrentSequences(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	id, err := f.deploy(t.Context(), staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, revision := range []int64{2, 3} {
		wg.Go(func() {
			errs <- withTx(t.Context(), f.pool, func(qtx *genDb.Queries) error {
				report := &agentv1.PlacementStatus{
					ObservedRevision: revision,
					DesiredReplicas:  1,
					Replicas:         []*agentv1.ReplicaReport{{Uid: uuid.NewString(), State: "Serving", PlacementRevision: revision}},
				}
				return ingestRollout(t.Context(), qtx, id, revision, report)
			})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var maxSeq int64
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*),max(seq) FROM deployment_activity WHERE deployment_id=$1`, id).
		Scan(&count, &maxSeq); err != nil ||
		int64(count) != maxSeq ||
		count != 4 {
		t.Fatalf("count=%d max=%d err=%v", count, maxSeq, err)
	}
}

func TestRolloutActivityLimit(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	id, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	if _, err := f.pool.Exec(
		ctx,
		`INSERT INTO deployment_activity(deployment_id,seq,revision,at,severity,message)
 SELECT $1,n,$2,now(),'info','seed' FROM generate_series(1,499) n`,
		id,
		p.DesiredRevision,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE deployments SET activity_seq=499 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(
		ctx,
		`INSERT INTO deployment_rollouts(deployment_id,revision,snapshot) VALUES ($1,$2,'{"count":499}')`,
		id,
		p.DesiredRevision,
	); err != nil {
		t.Fatal(err)
	}
	report := &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Replicas: []*agentv1.ReplicaReport{
			{Uid: "previous-replica", Name: "previous-pod", State: string(loco.ReplicaStopping)},
		},
	}
	server := f.agentServer()
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := activityRows(t, f, id); got != 501 {
		t.Fatalf("limit rows=%d want 501", got)
	}
	report.Replicas = nil
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := activityRows(t, f, id); got != 501 {
		t.Fatalf("replica entry escaped limit: %d", got)
	}
	report.Problem = &agentv1.ProblemReport{Reason: string(loco.ProblemOutOfMemory)}
	server.recordStatus(ctx, f.clusterID, 0, report)
	report.Problem = nil
	server.recordStatus(ctx, f.clusterID, 0, report)
	if got := activityRows(t, f, id); got != 501 {
		t.Fatalf("problem entry escaped limit: %d", got)
	}
}

func TestRolloutSnapshotNotifications(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	ctx := t.Context()
	id, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	listener, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, `LISTEN deployment_activity`); err != nil {
		t.Fatal(err)
	}
	report := &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Replicas: []*agentv1.ReplicaReport{
			{Uid: activityReplicaUID, State: string(loco.ReplicaServing), PlacementRevision: p.DesiredRevision},
		},
	}
	for _, detail := range []string{"initial detail", "changed detail"} {
		report.Replicas[0].Detail = detail
		f.agentServer().recordStatus(ctx, f.clusterID, 0, report)
		waitCtx, cancel := context.WithTimeout(ctx, time.Second)
		notification, waitErr := listener.Conn().WaitForNotification(waitCtx)
		cancel()
		if waitErr != nil {
			t.Fatal(waitErr)
		}
		if notification.Payload != id.String() {
			t.Fatalf("notification=%+v", notification)
		}
	}
	if got := activityRows(t, f, id); got != 1 {
		t.Fatalf("detail change wrote %d activity rows", got)
	}
	var before, after string
	query := `SELECT xmin::text FROM deployment_rollouts WHERE deployment_id=$1`
	if err := f.pool.QueryRow(ctx, query, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	f.agentServer().recordStatus(ctx, f.clusterID, 0, report)
	if err := f.pool.QueryRow(ctx, query, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("equal replay rewrote snapshot: %s -> %s", before, after)
	}
}
