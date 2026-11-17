package service

import (
	"testing"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const activityReplicaUID = "activity-replica"
const testServingMessage = "Serving traffic"

func TestDiffRollout(t *testing.T) {
	for _, tt := range []struct {
		name          string
		before, after loco.ReplicaState
		messages      []string
	}{
		{"first observation", "", loco.ReplicaServing, []string{testServingMessage}},
		{"skipped", loco.ReplicaScheduling, loco.ReplicaServing, []string{testServingMessage}},
		{"replay", loco.ReplicaServing, loco.ReplicaServing, nil},
		{"missing", loco.ReplicaServing, "", []string{"Stopped · no exit code seen"}},
		{"terminal missing", loco.ReplicaKilled, "", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			previous := rolloutSnapshot{}
			report := &agentv1.PlacementStatus{}
			if tt.before != "" {
				previous.Replicas = []*agentv1.ReplicaReport{{Uid: activityReplicaUID, State: string(tt.before)}}
			}
			if tt.after != "" {
				report.Replicas = []*agentv1.ReplicaReport{{Uid: activityReplicaUID, State: string(tt.after)}}
			}
			assertActivityMessages(t, diffRollout(previous, report), tt.messages)
		})
	}
}

func assertActivityMessages(t *testing.T, got []activityEntry, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries=%+v want %v", got, want)
	}
	for i, message := range want {
		if got[i].Message != message {
			t.Errorf("message=%q want %q", got[i].Message, message)
		}
	}
}

func TestDiffRolloutProblemAndScale(t *testing.T) {
	problem := &agentv1.ProblemReport{Reason: string(loco.ProblemCrashLoop)}
	message, _ := loco.ProblemMessage(loco.ProblemCrashLoop)
	for _, tt := range []struct {
		name     string
		previous rolloutSnapshot
		report   *agentv1.PlacementStatus
		want     []string
	}{
		{"scale", rolloutSnapshot{}, &agentv1.PlacementStatus{DesiredReplicas: 3}, []string{"Scaled to 3 replicas"}},
		{"problem", rolloutSnapshot{}, &agentv1.PlacementStatus{Problem: problem}, []string{message}},
		{"clear", rolloutSnapshot{Problem: problem}, &agentv1.PlacementStatus{}, []string{"Problem cleared"}},
		{
			"hit limit", rolloutSnapshot{Count: 499}, &agentv1.PlacementStatus{DesiredReplicas: 3},
			[]string{"Scaled to 3 replicas", activityLimitMessage},
		},
		{
			"problem beyond limit", rolloutSnapshot{Count: 501},
			&agentv1.PlacementStatus{DesiredReplicas: 3, Problem: problem}, []string{message},
		},
	} {
		t.Run(tt.name, func(t *testing.T) { assertActivityMessages(t, diffRollout(tt.previous, tt.report), tt.want) })
	}
}

func TestDiffRolloutPreviousGone(t *testing.T) {
	previous := rolloutSnapshot{
		Replicas: []*agentv1.ReplicaReport{
			{Uid: activityReplicaUID, State: string(loco.ReplicaStopping), PlacementRevision: 1},
		},
	}
	for _, replicas := range [][]*agentv1.ReplicaReport{
		nil,
		{{Uid: activityReplicaUID, State: string(loco.ReplicaStopped), PlacementRevision: 1}},
	} {
		entries := diffRollout(previous, &agentv1.PlacementStatus{ObservedRevision: 2, Replicas: replicas})
		if len(entries) != 2 || entries[1].Message != "Previous version gone" {
			t.Fatalf("entries=%+v", entries)
		}
	}
}

func TestDiffRolloutCrashLoop(t *testing.T) {
	previous := rolloutSnapshot{}
	rows, markers := 0, 0
	for i := range rolloutActivityLimit + 10 {
		state := loco.ReplicaFailing
		if i%2 == 1 {
			state = loco.ReplicaStarting
		}
		report := &agentv1.PlacementStatus{
			Replicas: []*agentv1.ReplicaReport{{Uid: activityReplicaUID, State: string(state), Restarts: int32(i + 1)}},
		}
		entries := diffRollout(previous, report)
		if i < rolloutActivityLimit {
			if len(entries) == 0 || entries[0].ReplicaUID != activityReplicaUID {
				t.Fatalf("transition %d not appended: %+v", i, entries)
			}
			rows++
		} else if len(entries) != 0 {
			t.Fatalf("transition escaped cap: %+v", entries)
		}
		for _, entry := range entries {
			if entry.Message == activityLimitMessage {
				markers++
			}
		}
		previous.Count += int32(len(entries))
		previous.Replicas = report.GetReplicas()
	}
	if rows != rolloutActivityLimit || markers != 1 {
		t.Fatalf("crash rows=%d markers=%d", rows, markers)
	}
}

func TestReplicaActivityMessages(t *testing.T) {
	for _, tt := range []struct {
		state             loco.ReplicaState
		message, severity string
	}{
		{loco.ReplicaScheduling, "Waiting for a node", activityInfo},
		{loco.ReplicaPulling, "Pulling image", activityInfo},
		{loco.ReplicaStarting, "Starting", activityInfo},
		{loco.ReplicaServing, "Serving traffic", activityOK},
		{loco.ReplicaFailing, "Failing", activityError},
		{loco.ReplicaStopping, "Stopping", activityInfo},
		{loco.ReplicaStopped, "Stopped", activityOK},
		{loco.ReplicaKilled, "Killed", activityWarn},
		{loco.ReplicaUnknown, "Replica state unknown", activityWarn},
	} {
		t.Run(string(tt.state), func(t *testing.T) {
			message, severity := replicaActivityMessage(&agentv1.ReplicaReport{State: string(tt.state)})
			if message != tt.message || severity != tt.severity {
				t.Fatalf("message=%q severity=%q", message, severity)
			}
		})
	}
}
