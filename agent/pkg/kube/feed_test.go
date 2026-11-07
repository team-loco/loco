package kube

import (
	"errors"
	"testing"
	"time"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const testBuildID = "b1"

var errForwardedWhileFrozen = errors.New("status forwarded while frozen")

func buildStatus(phase agentv1.BuildPhase) *agentv1.BuildStatus {
	return &agentv1.BuildStatus{BuildId: testBuildID, Phase: phase}
}

func TestFeedForwardsOnlyChangedStatuses(t *testing.T) {
	feed := NewFeed[*agentv1.BuildStatus]()
	feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_PENDING))

	var got []*agentv1.BuildStatus
	detach := feed.Attach(func(s *agentv1.BuildStatus) { got = append(got, s) })
	if len(got) != 1 || got[0].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_PENDING {
		t.Fatalf("attach replay = %v", got)
	}

	feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_PENDING))
	feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_RUNNING))
	feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_RUNNING))
	if len(got) != 2 || got[1].GetPhase() != agentv1.BuildPhase_BUILD_PHASE_RUNNING {
		t.Fatalf("statuses = %v, want one more, running", got)
	}

	detach()
	feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_FAILED))
	if len(got) != 2 {
		t.Fatal("status forwarded after detach")
	}

	feed.Forget(testBuildID)
	var replay []*agentv1.BuildStatus
	feed.Attach(func(s *agentv1.BuildStatus) { replay = append(replay, s) })
	if len(replay) != 0 {
		t.Fatalf("forgotten status still replayed: %v", replay)
	}
}

// freezeGrace is how long Freeze holds a concurrent Publish back before the test lets go. A
// correct Feed never forwards during Freeze, so a slow machine can only make the test pass.
const freezeGrace = 50 * time.Millisecond

func TestFeedHoldsStatusesBackWhileFrozen(t *testing.T) {
	feed := NewFeed[*agentv1.BuildStatus]()
	forwarded := make(chan struct{}, 1)
	feed.Attach(func(*agentv1.BuildStatus) { forwarded <- struct{}{} })

	err := feed.Freeze(func() error {
		go feed.Publish(testBuildID, buildStatus(agentv1.BuildPhase_BUILD_PHASE_RUNNING))
		select {
		case <-forwarded:
			return errForwardedWhileFrozen
		case <-time.After(freezeGrace):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-forwarded
}
