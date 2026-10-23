package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/team-loco/loco/agent/pkg/cluster"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	testHeartbeatInterval = 10 * time.Millisecond
	testHeartbeatWait     = 2 * time.Second
	testHeartbeatCount    = 3
	testServedDomain      = "apps.test"
)

type servedDomains []string

func (s servedDomains) Served(context.Context) ([]string, error) {
	return s, nil
}

func TestSendHeartbeatsEnqueuesOneAtOnceAndOneEveryInterval(t *testing.T) {
	inspector := cluster.NewInspector(fake.NewClientset(), "loco-system", "loco-controller")
	a := &Agent{
		inspector:         inspector,
		domains:           servedDomains{testServedDomain},
		buildsEnabled:     true,
		heartbeatInterval: testHeartbeatInterval,
	}
	ctx, cancel := context.WithCancel(t.Context())
	sent := make(chan *agentv1.SyncRequest)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		a.sendHeartbeats(ctx, func(msg *agentv1.SyncRequest) {
			select {
			case sent <- msg:
			case <-ctx.Done():
			}
		})
	}()

	for range testHeartbeatCount {
		select {
		case msg := <-sent:
			if !msg.GetHeartbeat().GetBuildsEnabled() {
				t.Fatalf("sent %v, want a heartbeat that reports builds enabled", msg)
			}
			if !slices.Equal(msg.GetHeartbeat().GetDomains(), []string{testServedDomain}) {
				t.Fatalf("sent %v, want a heartbeat that reports %s", msg, testServedDomain)
			}
		case <-time.After(testHeartbeatWait):
			t.Fatalf("no heartbeat within %s", testHeartbeatWait)
		}
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(testHeartbeatWait):
		t.Fatal("heartbeats kept going after the sync stream's context ended")
	}
}
