package main

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	testUsageInterval = time.Hour
	testUsageWait     = 2 * time.Second
)

type fakeMeter struct {
	wake chan struct{}
}

func (*fakeMeter) Report(context.Context) (*agentv1.UsageReport, error) {
	return &agentv1.UsageReport{}, nil
}

func (*fakeMeter) Recorded(time.Time) {}

func (f *fakeMeter) Wake() <-chan struct{} {
	return f.wake
}

func TestReportUsageReportsAtOnceAndWhenARunEnds(t *testing.T) {
	meter := &fakeMeter{wake: make(chan struct{}, 1)}
	a := &Agent{cfg: &Config{UsageInterval: testUsageInterval}, usage: meter}
	ctx, cancel := context.WithCancel(t.Context())
	sent := make(chan *agentv1.SyncRequest)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.reportUsage(ctx, func(msg *agentv1.SyncRequest) {
			select {
			case sent <- msg:
			case <-ctx.Done():
			}
		})
	}()

	receive := func(when string) {
		t.Helper()
		select {
		case msg := <-sent:
			if msg.GetUsage() == nil {
				t.Fatalf("sent %v %s, want a usage report", msg, when)
			}
		case <-time.After(testUsageWait):
			t.Fatalf("no usage report %s", when)
		}
	}
	receive("at once")
	meter.wake <- struct{}{}
	receive("after a run ended")

	cancel()
	<-stopped
}
