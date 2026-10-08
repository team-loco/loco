package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-loco/loco/agent/pkg/buildwatch"
)

const testDetectInterval = 10 * time.Millisecond

type fakeDetector struct {
	enabled atomic.Bool
	err     error
}

func (d *fakeDetector) BuildsEnabled(context.Context) (bool, error) {
	return d.enabled.Load(), d.err
}

func TestStartBuildsWithoutBuildSupportStartsNoWatch(t *testing.T) {
	start := func() (buildRunner, error) {
		t.Fatal("started a Build watch on a cluster without builds")
		return nil, nil
	}
	runner, err := startBuilds(false, start)
	if err != nil {
		t.Fatalf("startBuilds: %v", err)
	}
	if _, ok := runner.(buildwatch.Disabled); !ok {
		t.Fatalf("runner = %T, want buildwatch.Disabled", runner)
	}
}

func TestStartBuildsWithBuildSupportStartsTheWatch(t *testing.T) {
	started := false
	start := func() (buildRunner, error) {
		started = true
		return buildwatch.Disabled{}, nil
	}
	if _, err := startBuilds(true, start); err != nil {
		t.Fatalf("startBuilds: %v", err)
	}
	if !started {
		t.Fatal("the Build watch was not started on a cluster with builds")
	}
}

func TestWatchBuildSupportStopsTheAgentWhenSupportChanges(t *testing.T) {
	detector := &fakeDetector{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- watchBuildSupport(ctx, detector, false, testDetectInterval)
	}()
	detector.enabled.Store(true)
	err := <-done
	if !errors.Is(err, errBuildSupportChanged) {
		t.Fatalf("watchBuildSupport returned %v, want errBuildSupportChanged", err)
	}
}

func TestWatchBuildSupportKeepsRunningWhileSupportIsUnchanged(t *testing.T) {
	detector := &fakeDetector{}
	detector.enabled.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*testDetectInterval)
	defer cancel()
	err := watchBuildSupport(ctx, detector, true, testDetectInterval)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("watchBuildSupport returned %v, want it to run until the context ends", err)
	}
}
